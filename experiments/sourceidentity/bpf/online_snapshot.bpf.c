/* SPDX-License-Identifier: (MIT OR GPL-2.0-only) */
/* Finite, opt-in admission experiment. Attach BOTH links at the isolated
 * guest's cgroup2 root, before creating any target socket. The configured
 * source cgroup is a dedicated leaf. Never attach this experiment on the host.
 * No deletion, overwrite, LRU, pinning, or unbounded service is provided.
 */
#define HOOPA_GATE_WRAPPERS
#include "connect_snapshot.bpf.c"

static void (*gate_lock)(struct bpf_spin_lock *) = (void *)BPF_FUNC_spin_lock;
static void (*gate_unlock)(struct bpf_spin_lock *) = (void *)BPF_FUNC_spin_unlock;

struct writer_gate {
	struct bpf_spin_lock lock;
	__u32 closed;
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

struct online_tuple {
	__u64 generation;
	__u64 netns;
	__u32 client_ip4; /* context network byte order, not host address integer */
	__u32 server_ip4;
	__u32 client_port; /* host byte order */
	__u32 server_port;
};

struct online_binding {
	__u64 generation;
	__u64 netns;
	__u64 accepted_cookie;
	__u64 client_cookie;
	__u32 client_ip4;
	__u32 server_ip4;
	__u32 client_port;
	__u32 server_port;
	__u32 schema_version;
	__u32 reserved;
};

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_HASH);
	UINT_FIELD(max_entries, 1024);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, struct online_tuple);
	TYPE_FIELD(value, __u64);
} online_tuples SEC(".maps");

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_HASH);
	UINT_FIELD(max_entries, 1024);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u64);
	TYPE_FIELD(value, struct online_binding);
} online_bindings SEC(".maps");

_Static_assert(sizeof(struct writer_gate) == 40, "online gate ABI");
_Static_assert(sizeof(struct online_tuple) == 32, "online tuple ABI");
_Static_assert(sizeof(struct online_binding) == 56, "online binding ABI");

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

static ALWAYS_INLINE int no_fault(void)
{
	__u32 zero = 0;
	__u64 *fault = map_lookup(&capture_faults, &zero);
	return fault && *fault == 0;
}

SEC("cgroup/connect4")
int hoopa_connect(struct bpf_sock_addr *ctx)
{
	__u32 zero = 0;
	struct capture_config *cfg = map_lookup(&capture_config, &zero);
	if (!cfg || !cfg->generation || !cfg->cgroup || !cfg->netns ||
	    !cfg->destination_port || cfg->destination_port > 65535 || cfg->reserved)
		return 0;
	if (ctx->family != 2 || ctx->type != 1 || ctx->protocol != 6 ||
	    ctx->user_ip4 != __builtin_bswap32(0x7f000001U) ||
	    ctx->user_port != __builtin_bswap16((__u16)cfg->destination_port))
		return 1;
	struct writer_gate *gate = admit_writer();
	if (!gate)
		return 0;
	/* observe_connect records a fault on scope/read/NOEXIST failures. In this
	 * profile the wrapper also rejects the actual target connect, before ACK.
	 * Root coverage prevents an outside-leaf client bypassing this wrapper.
	 */
	observe_connect(ctx);
	__u64 cookie = socket_cookie(ctx);
	struct connect_observation *record = map_lookup(&connect_records, &cookie);
	int allowed = record && record->generation == cfg->generation &&
		record->socket_netns_cookie == cfg->netns &&
		record->cgroup == cfg->cgroup && no_fault();
	finish_writer(gate);
	return allowed ? 1 : 0;
}

static ALWAYS_INLINE int publish_binding(struct bpf_sock_ops *ctx)
{
	__u32 zero = 0;
	struct capture_config *cfg = map_lookup(&capture_config, &zero);
	if (!cfg || !cfg->generation || !cfg->cgroup || !cfg->netns ||
	    !cfg->destination_port || cfg->destination_port > 65535 || cfg->reserved)
		return failed();
	__u32 remote_port = __builtin_bswap32(ctx->remote_port);
	int active = ctx->op == BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB;
	if (ctx->family != 2 ||
	    ctx->local_ip4 != __builtin_bswap32(0x7f000001U) ||
	    ctx->remote_ip4 != __builtin_bswap32(0x7f000001U) ||
	    (active ? remote_port : ctx->local_port) != cfg->destination_port)
		return 1;
	__u64 cookie = socket_cookie(ctx);
	__u64 ns = netns_cookie(ctx);
	if (!ctx->is_fullsock || !cookie || !ns || ns != cfg->netns ||
	    !ctx->local_port || ctx->local_port > 65535 ||
	    !remote_port || remote_port > 65535)
		return failed();
	struct online_tuple key = {
		.generation = cfg->generation, .netns = ns,
		.client_ip4 = active ? ctx->local_ip4 : ctx->remote_ip4,
		.server_ip4 = active ? ctx->remote_ip4 : ctx->local_ip4,
		.client_port = active ? ctx->local_port : remote_port,
		.server_port = cfg->destination_port,
	};
	__u64 client_cookie = cookie;
	if (!active) {
		__u64 *found = map_lookup(&online_tuples, &key);
		if (!found || !*found || *found == cookie)
			return failed();
		client_cookie = *found;
	}
	struct connect_observation *record = map_lookup(&connect_records, &client_cookie);
	if (!record || record->generation != cfg->generation ||
	    record->socket_netns_cookie != ns || record->cgroup != cfg->cgroup)
		return failed();
	if (active) {
		if (map_update(&online_tuples, &key, &client_cookie, INSERT_ONLY))
			return failed();
		return 1;
	}
	struct online_binding binding = {
		.generation = cfg->generation, .netns = ns,
		.accepted_cookie = cookie, .client_cookie = client_cookie,
		.client_ip4 = key.client_ip4, .server_ip4 = key.server_ip4,
		.client_port = key.client_port, .server_port = key.server_port,
		.schema_version = 1,
	};
	if (map_update(&online_bindings, &cookie, &binding, INSERT_ONLY))
		return failed();
	return 1;
}

SEC("sockops")
int hoopa_lifecycle(struct bpf_sock_ops *ctx)
{
	if (ctx->op != BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB &&
	    ctx->op != BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB)
		return 1;
	struct writer_gate *gate = admit_writer();
	if (!gate)
		return 1;
	int result = publish_binding(ctx);
	finish_writer(gate);
	return result;
}

/* Same owner closes admission before invoking this program. Never attach it. */
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
