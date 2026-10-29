/* SPDX-License-Identifier: (MIT OR GPL-2.0-only) */
/* T132 lifecycle observation only. No tuple index or identity association.
 * Keep the capture-only profile unchanged; this object adds a sockops program
 * sharing its config, connect observations, and whole-run fault counter.
 */
#include "connect_snapshot.bpf.c"

static long (*set_callback_flags)(struct bpf_sock_ops *, int) =
	(void *)BPF_FUNC_sock_ops_cb_flags_set;

/* Both addresses retain the context's network byte order. Ports are stored
 * in host byte order. These are socket-local endpoints, not a normalized join
 * key. The opt-in namespaced profile measures the socket's namespace with
 * get_netns_cookie; it never substitutes the callback task's namespace.
 * Neither profile infers a process identity from the sockops callback.
 */
struct established_observation {
	__u64 generation;
	__u64 observed_boot_ns;
	__u64 socket_cookie;
	__u32 schema_version;
	__u32 callback_op;
	__u32 local_ip4;
	__u32 remote_ip4;
	__u32 local_port;
	__u32 remote_port;
#ifdef HOOPA_NAMESPACED
	__u64 socket_netns_cookie;
#endif
};

/* TCP_CLOSE is a state-transition observation, not close(2), object destruction,
 * process exit, proof that the peer has closed, or permission to reclaim a key.
 */
struct close_observation {
	__u64 generation;
	__u64 observed_boot_ns;
	__u64 socket_cookie;
	__u32 schema_version;
	__u32 old_state;
	__u32 new_state;
	__u32 reserved;
};

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_HASH);
	UINT_FIELD(max_entries, 2048);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u64);
	TYPE_FIELD(value, struct established_observation);
} established_records SEC(".maps");

struct {
	UINT_FIELD(type, BPF_MAP_TYPE_HASH);
	UINT_FIELD(max_entries, 2048);
	UINT_FIELD(map_flags, MAP_RDONLY_USER);
	TYPE_FIELD(key, __u64);
	TYPE_FIELD(value, struct close_observation);
} close_records SEC(".maps");

#ifdef HOOPA_NAMESPACED
_Static_assert(sizeof(struct established_observation) == 56, "namespaced established ABI");
_Static_assert(__builtin_offsetof(struct established_observation, socket_netns_cookie) == 48,
	       "namespaced established extension offset");
#else
_Static_assert(sizeof(struct established_observation) == 48, "established ABI");
#endif
_Static_assert(sizeof(struct close_observation) == 40, "close ABI");

static ALWAYS_INLINE int observe_lifecycle(struct bpf_sock_ops *ctx)
{
	__u32 zero = 0;
	__u32 op = ctx->op;
	__u64 cookie;
	struct capture_config *cfg;

	if (op != BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB &&
	    op != BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB &&
	    op != BPF_SOCK_OPS_STATE_CB)
		return 1;
	if (ctx->family != 2)
		return 1;
	cfg = map_lookup(&capture_config, &zero);
	if (!cfg || !cfg->generation || !cfg->cgroup || !cfg->netns ||
	    !cfg->destination_port || cfg->destination_port > 65535 || cfg->reserved)
		return failed();

	cookie = socket_cookie(ctx);
	if (!cookie)
		return failed();
	if (op == BPF_SOCK_OPS_STATE_CB) {
		struct established_observation *established;
		struct close_observation out = {};

		/* Other observers may enable STATE_CB on sockets we did not record. */
		if (ctx->args[1] != TCP_CLOSE)
			return 1;
		established = map_lookup(&established_records, &cookie);
		if (!established)
			return 1;
		if (established->generation != cfg->generation)
			return failed();
		out.generation = cfg->generation;
		out.observed_boot_ns = boot_ns();
		out.socket_cookie = cookie;
		out.schema_version = 1;
		out.old_state = ctx->args[0];
		out.new_state = ctx->args[1];
		if (map_update(&close_records, &cookie, &out, INSERT_ONLY))
			return failed();
		return 1;
	}

	/* sockops remote_port is a network-order 32-bit context value, unlike
	 * sock_addr.user_port. Convert the full word, not its low 16 bits.
	 */
	__u32 remote_port = __builtin_bswap32(ctx->remote_port);
	if (ctx->local_ip4 != __builtin_bswap32(0x7f000001U) ||
	    ctx->remote_ip4 != __builtin_bswap32(0x7f000001U))
		return 1;
	if ((op == BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB &&
	     remote_port != cfg->destination_port) ||
	    (op == BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB &&
	     ctx->local_port != cfg->destination_port))
		return 1;
	if (!ctx->is_fullsock || !ctx->local_port || ctx->local_port > 65535 ||
	    !remote_port || remote_port > 65535)
		return failed();
#ifdef HOOPA_NAMESPACED
	/* Read from this actual full socket for both active and passive callbacks.
	 * A copied config value would not establish the accepted socket's netns.
	 */
	__u64 observed_netns = netns_cookie(ctx);
	if (!observed_netns || observed_netns != cfg->netns)
		return failed();
#endif
	if (op == BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB) {
		struct connect_observation *connect = map_lookup(&connect_records, &cookie);
		if (!connect || connect->generation != cfg->generation ||
		    connect->socket_netns_cookie != cfg->netns || connect->cgroup != cfg->cgroup)
			return failed();
	}

	struct established_observation out = {};
	out.generation = cfg->generation;
	out.observed_boot_ns = boot_ns();
	out.socket_cookie = cookie;
	out.schema_version =
#ifdef HOOPA_NAMESPACED
		2;
	out.socket_netns_cookie = observed_netns;
#else
		1;
#endif
	out.callback_op = op;
	out.local_ip4 = ctx->local_ip4;
	out.remote_ip4 = ctx->remote_ip4;
	out.local_port = ctx->local_port;
	out.remote_port = remote_port;
	if (map_update(&established_records, &cookie, &out, INSERT_ONLY))
		return failed();
	/* Preserve existing flags. The return is zero only when all requested flags
	 * were accepted. A failure invalidates the run, including this observation.
	 */
	if (set_callback_flags(ctx, ctx->bpf_sock_ops_cb_flags | BPF_SOCK_OPS_STATE_CB_FLAG))
		return failed();
	return 1;
}

#ifndef HOOPA_GATE_WRAPPERS
SEC("sockops")
int hoopa_lifecycle(struct bpf_sock_ops *ctx)
{
	return observe_lifecycle(ctx);
}
#endif
