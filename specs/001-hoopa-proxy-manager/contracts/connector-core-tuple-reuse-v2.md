# 固定核心的 TCP 四元组复用拒绝（V2）

2026-10-24，修正 V1 的状态字段解释；V1 与首轮原始失败均保留。V1 把 `accepted_tcp_connections` 误作累计值，实际固定核心 `hoopa_guard_linux.go` 返回 `len(r.conns)`，表示当前仍持有的连接数。首轮第一流成功关闭后，等待累计值 1 超时，第二客户端尚未启动。本版本只修正这一判据及失败诊断，不改变身份、字节或拒绝要求。

T132 的有限负向子步骤，固定 001–009 核心与原 online BPF 不变。当前 tuple 表只插入、不覆盖、不回收；复用应触发持续故障并拒绝新身份，不能预期第二流正常通过，更不能用旧 cookie/PID 代填。

单个隔离 VM、同一核心/捕获世代内，两个自有独立进程依次用 `SO_REUSEADDR` 绑定 `127.0.0.1:31001`，连接原入口 `127.0.0.1:18993`。第一进程完成 HTTP CONNECT → node:a → alpha.test 的 33 字节双向交换、EOF 及正常终末后，用 `SO_LINGER(1,0)` 关闭并正常退出。wait/reap 自有子进程后等待 1.2 秒；读取记录 guest TCP TIME_WAIT 配置，不修改宿主网络配置。第二进程复用相同地址/端口，保存 connect 前后的 PID/leader start、cookie、netns、端点及单调时刻。必须实际证明四元组相等、两个 socket cookie 和进程实例不同。

第二次 TCP connect 可成功，随后核心 accept 并在协议读取之前拒绝。第二客户端不发送代理握手或应用数据，只等待有界 EOF/reset；普通超时、bind 失败或 connect 失败不能替代所需的 accept/准入拒绝证据。原核心受保护状态中，三个静稳阶段（ready、首流关闭后、第二连接拒绝后）的当前连接数 `accepted_tcp_connections` 均应为 0；累计 admission 的 admitted 分别为 0/1/1、rejected 为 0/0/1，最后 revoked=true。两次实际成功 connect 与同一 owner 的累计 admitted+rejected=2 共同验证本轮两次准入调用；不能用当前连接数推断累计 accept。已有第一流不得改写。第二流没有 opened/ready/closed 或实际 node/target 记录，第一流仍严格满足原 connector、实际线路、ready 和双向 33B 终末。不能从“客户端未写”单独推断核心未读；还须由原 Accept→Admit 代码路径及拒绝状态共同解释边界。

有界状态等待保存原始状态采样/最后值及超时原因，不能事后补造旧失败时未保存的状态。各阶段注册与原始材料独立导出；核对第一流的同 flow/attempt 事件及来源重开，记录全部错误。默认执行拒绝、scope/模式/整数/状态/tuple/cookie/多余流/超时篡改和旧成功路径的离线检查完成后才构建/运行。使用专用 virtio 证据通道和独立内核日志，不删除失败材料。

通过只证明本次顺序四元组复用的失败关闭行为，不证明全部 TCP 回调交错、事件覆盖或长期回收。普通 TCP active/passive callback 顺序与特殊握手路径须另做固定源码审查；容量/地址复用导致的可用性限制仍须后续设计解决，不将本轮作为生产长时间运行支持。T132/T137 与正式能力门槛保持。
