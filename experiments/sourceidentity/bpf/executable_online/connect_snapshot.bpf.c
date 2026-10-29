/* SPDX-License-Identifier: (MIT OR GPL-2.0-only) */
/* T132 spike A: raw connect observations only. Not a loader or identity backend.
 * Compile against a private header generated from the selected kernel BTF.
 * Every path returns 1: this observer never changes connect policy.
 */
#include "vmlinux.h"

#define SEC(name) __attribute__((section(name), used))
#define UINT_FIELD(name, value) int (*name)[value]
#define TYPE_FIELD(name, type) type *name
#define ALWAYS_INLINE inline __attribute__((always_inline))
/* UAPI macro constants are not represented in the generated BTF header. */
#define MAP_RDONLY_USER (1U << 3)
#define MAP_RDONLY_PROG (1U << 7)
#define INSERT_ONLY 1
#define CORE_READ(dst, field)                                                   \
	read_kernel(&(dst), sizeof(dst), __builtin_preserve_access_index(&(field)))

static void *(*map_lookup)(void *, const void *) = (void *)BPF_FUNC_map_lookup_elem;
static long (*map_update)(void *, const void *, const void *, __u64) =
	(void *)BPF_FUNC_map_update_elem;
static __u64 (*pid_tgid)(void) = (void *)BPF_FUNC_get_current_pid_tgid;
static __u64 (*current_task)(void) = (void *)BPF_FUNC_get_current_task;
static long (*read_kernel)(void *, __u32, const void *) =
	(void *)BPF_FUNC_probe_read_kernel;
static long (*current_comm)(void *, __u32) = (void *)BPF_FUNC_get_current_comm;
static __u64 (*socket_cookie)(void *) = (void *)BPF_FUNC_get_socket_cookie;
static __u64 (*netns_cookie)(void *) = (void *)BPF_FUNC_get_netns_cookie;
static __u64 (*cgroup_id)(void) = (void *)BPF_FUNC_get_current_cgroup_id;
static __u64 (*boot_ns)(void) = (void *)BPF_FUNC_ktime_get_boot_ns;

/* A future explicit loader must populate and BPF_MAP_FREEZE this map before
 * attach. Reconfiguration needs new maps/programs and a new generation.
 */
struct capture_config {
	__u64 generation;
	__u64 cgroup;
	__u64 netns;
	__u32 destination_port; /* host byte order; exact 127.0.0.1 TCP endpoint */
	__u32 reserved;
};

/* The observations below have not been validated by a kernel run. In
 * particular, copying comm does not establish an atomic name snapshot.
 */
struct connect_observation {
	__u64 generation;
	__u64 observed_boot_ns;
	__u64 client_cookie;
	__u64 socket_netns_cookie;
	__u64 cgroup;
	__u64 leader_start_boottime_ns;
	__u32 schema_version;
	__u32 tgid_initial;
	__u32 caller_tid_initial;
	__u32 active_pidns_inum;
	__u32 leader_pid_initial;
	__u32 caller_comm_status; /* 0 unavailable, 1 unverified observation */
	__u32 leader_comm_status; /* 0 unavailable, 1 unverified observation */
	__u32 reserved;
	char caller_comm[16];
	char leader_comm[16];
};

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_ARRAY);
	UINT_FIELD(max_entries, 1);
	UINT_FIELD(map_flags, MAP_RDONLY_PROG);
	TYPE_FIELD(key, __u32);
	TYPE_FIELD(value, struct capture_config);
} capture_config SEC(".maps");

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_HASH);
	UINT_FIELD(max_entries, 1024);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u64);
	TYPE_FIELD(value, struct connect_observation);
} connect_records SEC(".maps");

/* A nonzero value invalidates the whole capture run, including earlier records.
 * No reset, eviction, timeout cleanup or partial-success assertion in spike A.
 */
struct {
	UINT_FIELD(type, BPF_MAP_TYPE_ARRAY);
	UINT_FIELD(max_entries, 1);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u32);
	TYPE_FIELD(value, __u64);
} capture_faults SEC(".maps");

_Static_assert(sizeof(struct capture_config) == 32, "capture config ABI");
_Static_assert(sizeof(struct connect_observation) == 112, "capture record ABI");

static ALWAYS_INLINE int failed(void)
{
	__u32 zero = 0;
	__u64 *faults = map_lookup(&capture_faults, &zero);
	if (faults)
		__sync_fetch_and_add(faults, 1);
	return 1;
}

static ALWAYS_INLINE int observe_connect(struct bpf_sock_addr *ctx)
{
	__u32 zero = 0;
	struct capture_config *cfg = map_lookup(&capture_config, &zero);
	struct connect_observation out = {};
	struct task_struct *task, *leader = 0;
	struct pid *leader_pid = 0;
	struct pid_namespace *pidns = 0;
	__u32 level = 0, leader_tgid = 0;
	__u64 ids;

	if (!cfg || !cfg->generation || !cfg->cgroup || !cfg->netns ||
	    !cfg->destination_port || cfg->destination_port > 65535 || cfg->reserved)
		return failed();
	/* AF_INET=2, SOCK_STREAM=1, IPPROTO_TCP=6. UAPI ports are network order. */
	if (ctx->family != 2 || ctx->type != 1 || ctx->protocol != 6 ||
	    ctx->user_ip4 != __builtin_bswap32(0x7f000001U) ||
	    ctx->user_port != __builtin_bswap16((__u16)cfg->destination_port))
		return 1;
	out.cgroup = cgroup_id();
	out.socket_netns_cookie = netns_cookie(ctx);
	if (out.cgroup != cfg->cgroup || out.socket_netns_cookie != cfg->netns)
		return failed();

	out.generation = cfg->generation;
	out.schema_version = 1;
	out.observed_boot_ns = boot_ns();
	out.client_cookie = socket_cookie(ctx);
	ids = pid_tgid();
	out.tgid_initial = ids >> 32;
	out.caller_tid_initial = (__u32)ids;
	task = (void *)current_task();
	if (!task || !out.client_cookie || !out.tgid_initial || !out.caller_tid_initial)
		return failed();
	if (CORE_READ(leader, task->group_leader) || !leader ||
	    CORE_READ(leader_tgid, leader->tgid) ||
	    CORE_READ(out.leader_pid_initial, leader->pid) ||
	    CORE_READ(out.leader_start_boottime_ns, leader->start_boottime) ||
	    CORE_READ(leader_pid, leader->thread_pid) || !leader_pid ||
	    CORE_READ(level, leader_pid->level))
		return failed();
	/* The first finite spike supports the initial PID namespace only. Do not
 * substitute pid_ns_for_children or guess dynamic namespace coordinates.
 */
	if (level != 0 || leader_tgid != out.tgid_initial ||
	    out.leader_pid_initial != out.tgid_initial || !out.leader_start_boottime_ns)
		return failed();
	if (CORE_READ(pidns, leader_pid->numbers[0].ns) || !pidns ||
	    CORE_READ(out.active_pidns_inum, pidns->ns.inum) || !out.active_pidns_inum)
		return failed();

	if (current_comm(out.caller_comm, sizeof(out.caller_comm)) == 0)
		out.caller_comm_status = 1;
	if (CORE_READ(out.leader_comm, leader->comm) == 0)
		out.leader_comm_status = 1;
	out.caller_comm[15] = 0;
	out.leader_comm[15] = 0;
	/* A repeated cookie, including socket reuse after failed connect, is a
 * capture conflict. Preserve the first observation and invalidate the run.
 */
	if (map_update(&connect_records, &out.client_cookie, &out, INSERT_ONLY))
		return failed();
	return 1;
}

#ifndef HOOPA_GATE_WRAPPERS
SEC("cgroup/connect4")
int hoopa_connect(struct bpf_sock_addr *ctx)
{
	return observe_connect(ctx);
}
#endif

char LICENSE[] SEC("license") = "Dual MIT/GPL";
