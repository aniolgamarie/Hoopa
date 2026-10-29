/* SPDX-License-Identifier: (MIT OR GPL-2.0-only) */
/* Candidate writer-stop protocol, not runtime-verified identity or completeness.
 * The shared lock protects admission/exit/stop only. No observation helper is
 * called while holding it. All observation/fault writes occur between admission
 * and exit, including every early return in the original observation bodies.
 */
#define HOOPA_GATE_WRAPPERS
#include "lifecycle_snapshot.bpf.c"

static void (*gate_lock)(struct bpf_spin_lock *) = (void *)BPF_FUNC_spin_lock;
static void (*gate_unlock)(struct bpf_spin_lock *) = (void *)BPF_FUNC_spin_unlock;

struct writer_gate {
	struct bpf_spin_lock lock;
	__u32 closed; /* zero initially, monotonically transitions to one */
	__u64 active;
	__u64 admitted;
	__u64 completed;
	__u32 poison;
	__u32 reserved;
};

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_ARRAY);
	UINT_FIELD(max_entries, 1);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u32);
	TYPE_FIELD(value, struct writer_gate);
} capture_gate SEC(".maps");

_Static_assert(sizeof(struct writer_gate) == 40, "gate ABI");

static ALWAYS_INLINE struct writer_gate *admit_writer(void)
{
	__u32 zero = 0;
	struct writer_gate *gate = map_lookup(&capture_gate, &zero);
	int admitted = 0;

	if (!gate)
		return 0;
	gate_lock(&gate->lock);
	if (!gate->closed) {
		if (gate->admitted == ~0ULL) {
			gate->closed = 1;
			gate->poison = 1;
		} else {
			gate->admitted++;
			gate->active++;
			admitted = 1;
		}
	}
	gate_unlock(&gate->lock);
	return admitted ? gate : 0;
}

static ALWAYS_INLINE void finish_writer(struct writer_gate *gate)
{
	gate_lock(&gate->lock);
	if (!gate->active || gate->completed >= gate->admitted) {
		gate->poison = 1;
		gate->closed = 1;
	} else {
		gate->active--;
		gate->completed++;
	}
	gate_unlock(&gate->lock);
}

SEC("cgroup/connect4")
int hoopa_connect(struct bpf_sock_addr *ctx)
{
	struct writer_gate *gate = admit_writer();
	if (!gate)
		return 1;
	int result = observe_connect(ctx);
	finish_writer(gate);
	return result;
}

SEC("sockops")
int hoopa_lifecycle(struct bpf_sock_ops *ctx)
{
	struct writer_gate *gate = admit_writer();
	if (!gate)
		return 1;
	int result = observe_lifecycle(ctx);
	finish_writer(gate);
	return result;
}

/* Never attach this program. A future authorized runner invokes it once via
 * BPF_PROG_TEST_RUN, repeat=1, with a dummy skb. It touches only the gate map,
 * does not emit packets, and cannot reset counts or reopen this generation.
 */
SEC("tc")
int hoopa_stop(struct __sk_buff *ctx)
{
	__u32 zero = 0;
	struct writer_gate *gate = map_lookup(&capture_gate, &zero);
	if (!gate)
		return 1;
	gate_lock(&gate->lock);
	gate->closed = 1;
	gate_unlock(&gate->lock);
	return 0;
}
