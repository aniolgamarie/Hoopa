# 0010 核心的普通转发与 TFO 拒绝回归

状态：2026-10-24 运行前登记。新制品 SHA256 `e9dd96f3816bcc050bfcc472f8cd578bd4bf9a26f09923790df5481ebdadd9be`，固定 Mihomo v1.19.26 commit `fc8c5a24b16991f98cd736950c17d1aa306a5041` 加001–010。旧001–009制品/证据保留，新制品不继承旧hash的passed。

仅限既有无NIC、无宿主共享、固定6.18.54.1/kernel image hash的隔离guest。BPF、libbpf、11FD移交、source v3与两条独立线路fixture保持既有版本。主机只构建/启动自有VM及读取导出，不改变宿主内核、cgroup或系统代理。

普通正向独立VM：两个独立客户端，HTTP CONNECT访问alpha.test经node:a，SOCKS5访问beta.test经node:b。分别完成认证握手，send33→SHUT_WR→读取原33B和EOF，禁止推迟SHUT_WR到应答之后。核对原PID/start/cookie、opened与同flow实际线路、forward_setup_v2 ready和精确双向33B终末。累计admitted2/rejected0/revokedfalse，结束时live gauge0，core SIGTERM0。该项不证明TFO拒绝。

TFO负向独立VM：guest tcp_fastopen从原值临时设0x607，结束恢复。首普通HTTP alpha流使用127.0.0.1:31001→18993，33/33完整终末，abortive close、reap并等待至少1.2s，保存完整source prefix。第二不同PID/start/cookie使用同tuple，带TCP_FASTOPEN_NO_COOKIE=1，以有3秒SO_SNDTIMEO的blocking sendmsg(MSG_FASTOPEN)一次发送有效认证HTTP CONNECT beta.test:18995首部和33Btoken。必须完整send返回且原client TCP_INFO options含SYN_DATA32，未满足即失败，不能换普通connect充数。

第二客户端只能观察EOF或ECONNRESET，没有HTTP回应；节点/目标没有第二token或字节，source没有第二opened/flow且旧prefix不变。累计admitted1/rejected1/revokedtrue、core SIGTERM0。保存原send字节/ret、TCP_INFO raw、socket选项、cookie/端点/时间及sysctl恢复。该项联合源码的检查顺序与独立TFO_CHILD观察验证拒绝行为；它不单独记录或证明具体拒绝分支，也不宣称复现旧核心错误归属竞态。

每轮由host独立核对原始事件解析/恢复、全部导出hash、fixture token收支、kernel/BTF/制品/配置pin、证据通道无混入、子进程/线程/FD清理。失败保存为新run，不修改旧结果或将工具准备数当作真实流数。正式identity仍unverified、G01新样本0，T132/T137保持未完成。
