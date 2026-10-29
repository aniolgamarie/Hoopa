/* Combined finite online association and same-call executable capture candidate. */
#include "vmlinux.h"
#include "bpf_core_read.h"
#undef SEC
static __always_inline int hoopa_executable_allow(struct bpf_sock_addr *ctx);
#include "online_snapshot.bpf.c"

extern struct file *bpf_get_task_exe_file(struct task_struct *) __ksym;
extern int bpf_path_d_path(const struct path *, char *, size_t) __ksym;
extern void bpf_put_file(struct file *) __ksym;

struct hoopa_call { __u64 token; };
struct hoopa_exe_record {
    __u64 cookie;
    __u32 tgid, tid;
    __s32 path_result;
    __u32 consumed;
    __u64 boot_ns, call_token;
    char path[256];
};
struct hoopa_exe_state {
    __u64 captures, connects, allowed, refused, fault, next_token, entries, exits;
};
struct { __uint(type, BPF_MAP_TYPE_HASH); __uint(max_entries, 1024);
    __uint(map_flags, BPF_F_RDONLY); __type(key, __u64);
    __type(value, struct hoopa_exe_record); } exe_records SEC(".maps");
struct { __uint(type, BPF_MAP_TYPE_ARRAY); __uint(max_entries, 1);
    __uint(map_flags, BPF_F_RDONLY); __type(key, __u32);
    __type(value, struct hoopa_exe_state); } exe_state SEC(".maps");
struct { __uint(type, BPF_MAP_TYPE_TASK_STORAGE); __uint(map_flags, BPF_F_NO_PREALLOC);
    __type(key, int); __type(value, struct hoopa_call); } calls SEC(".maps");
_Static_assert(sizeof(struct hoopa_exe_record) == 296, "record ABI v2");
_Static_assert(sizeof(struct hoopa_exe_state) == 64, "state ABI v2");

static __always_inline struct hoopa_exe_state *own(void)
{
    __u32 zero = 0;
    struct capture_config *c = bpf_map_lookup_elem(&capture_config, &zero);
    if (!c || !c->generation || !c->cgroup || !c->netns || c->reserved ||
        !c->destination_port || c->destination_port > 65535 ||
        c->cgroup != bpf_get_current_cgroup_id()) return 0;
    return bpf_map_lookup_elem(&exe_state, &zero);
}

SEC("raw_tp/sys_enter")
int call_enter(struct bpf_raw_tracepoint_args *ctx)
{
    struct hoopa_exe_state *s = own();
    if (!s) return 0;
    struct task_struct *task = bpf_get_current_task_btf();
    struct hoopa_call *call = bpf_task_storage_get(&calls, task, 0, 0);
    if (call) call->token = 0; /* Every observed entry invalidates old state. */
    if (ctx->args[1] != 42 || s->fault) return 0;
    __u32 status = 0;
    struct pt_regs *regs = (void *)ctx->args[0];
    unsigned long cs = 0;
    if (bpf_core_read(&status, sizeof(status), &task->thread_info.status) ||
        bpf_core_read(&cs, sizeof(cs), &regs->cs)) {
        __sync_fetch_and_or(&s->fault, 1);
        return 0;
    }
    /* Fixed x86_64 TS_COMPAT bit and standard native user CS; x32 id is tagged. */
    if ((status & 2) || cs != 0x33) return 0;
    if (!call) call = bpf_task_storage_get(&calls, task, 0, BPF_LOCAL_STORAGE_GET_F_CREATE);
    if (!call) { __sync_fetch_and_or(&s->fault, 1); return 0; }
    /* Saturating allocation: never expose a wrapped/reused identifier. */
    for (int attempt = 0; attempt < 8; attempt++) {
        __u64 previous = s->next_token;
        if (previous == ~0ULL) break;
        if (__sync_val_compare_and_swap(&s->next_token, previous, previous + 1) != previous)
            continue;
        call->token = previous + 1;
        __sync_fetch_and_add(&s->entries, 1);
        return 0;
    }
    __sync_fetch_and_or(&s->fault, 1);
    return 0;
}

SEC("raw_tp/sys_exit")
int call_exit(struct bpf_raw_tracepoint_args *ctx)
{
    struct hoopa_exe_state *s = own();
    if (!s) return 0;
    struct hoopa_call *call = bpf_task_storage_get(&calls, bpf_get_current_task_btf(), 0, 0);
    if (call && call->token) {
        call->token = 0;
        __sync_fetch_and_add(&s->exits, 1);
    }
    return 0;
}

SEC("lsm/socket_connect")
int capture_exe(unsigned long long *ctx)
{
    int prior = (int)ctx[3];
    if (prior) return prior;
    struct hoopa_exe_state *s = own();
    if (!s) return 0;
    struct socket *sock = (void *)ctx[0];
    void *address = (void *)ctx[1];
    int addrlen = (int)ctx[2];
    struct { __u16 family, port; __u32 address; } target = {};
    if (addrlen < 16 || bpf_probe_read_kernel(&target, sizeof(target), address)) return 0;
    __u32 zero = 0;
    struct capture_config *cfg = bpf_map_lookup_elem(&capture_config, &zero);
    if (!cfg) return 0;
    if (target.family != 2 || target.port != __builtin_bswap16((__u16)cfg->destination_port) ||
        target.address != __builtin_bswap32(0x7f000001U) || sock->type != 1) return 0;
    struct hoopa_call *call = bpf_task_storage_get(&calls, bpf_get_current_task_btf(), 0, 0);
    if (!call || !call->token || s->fault) return 0;
    __u64 key = bpf_get_socket_cookie(sock->sk), id = bpf_get_current_pid_tgid();
    if (!key) { __sync_fetch_and_or(&s->fault, 1); return 0; }
    struct hoopa_exe_record r = { .cookie = key, .tgid = id >> 32, .tid = (__u32)id,
        .path_result = -2, .boot_ns = bpf_ktime_get_boot_ns(), .call_token = call->token };
    struct file *file = bpf_get_task_exe_file(bpf_get_current_task_btf());
    if (file) {
        r.path_result = bpf_path_d_path(&file->f_path, r.path, sizeof(r.path));
        bpf_put_file(file);
    }
    if (bpf_map_update_elem(&exe_records, &key, &r, BPF_NOEXIST)) __sync_fetch_and_or(&s->fault, 1);
    else __sync_fetch_and_add(&s->captures, 1);
    return 0;
}

static __always_inline int hoopa_executable_allow(struct bpf_sock_addr *ctx)
{
    struct hoopa_exe_state *s = own();
    if (!s) return 0;
    __u32 zero = 0;
    struct capture_config *cfg = bpf_map_lookup_elem(&capture_config, &zero);
    if (!cfg) return 0;
    if (ctx->family != 2 || ctx->type != 1 || ctx->protocol != 6 ||
        ctx->user_ip4 != __builtin_bswap32(0x7f000001U) ||
        ctx->user_port != __builtin_bswap16((__u16)cfg->destination_port)) return 0;
    __sync_fetch_and_add(&s->connects, 1);
    struct hoopa_call *call = bpf_task_storage_get(&calls, bpf_get_current_task_btf(), 0, 0);
    struct tcp_sock *tcp = bpf_skc_to_tcp_sock(ctx->sk);
    __u64 key = bpf_get_socket_cookie(ctx), id = bpf_get_current_pid_tgid();
    struct hoopa_exe_record *r = bpf_map_lookup_elem(&exe_records, &key);
    if (!call || !call->token || !tcp || !key || !r || r->cookie != key ||
        r->tgid != (id >> 32) || r->tid != (__u32)id || r->path_result <= 0 ||
        r->path_result > 256 || s->fault || r->call_token != call->token) goto reject;
    /* Deferred TFO must be denied on its first connect, before a later write. */
    if (tcp->fastopen_req || BPF_CORE_READ_BITFIELD(tcp, fastopen_connect)) goto reject;
    if (__sync_val_compare_and_swap(&r->consumed, 0, 1) != 0) goto reject;
    __sync_fetch_and_add(&s->allowed, 1);
    return 1;
reject:
    __sync_fetch_and_add(&s->refused, 1);
    return 0;
}

