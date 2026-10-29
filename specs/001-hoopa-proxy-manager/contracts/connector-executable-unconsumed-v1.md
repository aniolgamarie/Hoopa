# 已捕获但未消费记录的同 socket 反例

状态：2026-10-24 预登记；尚未运行。使用已固定的 consumed 对象 `feda13ebad10bd057b5e58bf0eb2e33ebeb3afdddfd71bf2525c3acd36c837dd`，不改变对象或解释旧 profile。

固定源码中普通 connect 先经过 socket_connect LSM，再由 TCP 的 SS_CONNECTED 分支返回 EISCONN，该分支不执行 cgroup pre-connect。本实验检验因此留下的 consumed=0 快照是否会被后来另一尝试使用；预期观察到缺口不是生产验收通过。

仅限固定6.18.54.1、PID1/root/`hoopa_lab=t132`的无NIC、无宿主共享隔离guest。先在挂接任何BPF前，于127.0.0.1:18993建立原client/listener/accepted TCP，确认原FD、双方cookie/端点及ESTABLISHED；不发送应用字节。首次建立必须实际blocking connect，保存无O_NONBLOCK、Python timeout=None和SO_SNDTIMEO=3秒的原始读回；不能用非阻塞connect后SO_ERROR就绪推断socket层已是SS_CONNECTED。再加载、freeze配置并挂接LSM和gate，确认初始状态/记录为空。该细节在首次VM前补入，未改变后续预期errno。

1. 同一已连接client FD再次普通connect，必须返回EISCONN(106)。捕获记录consumed=0，累计captures/connects/allowed/refused/fault=1/0/0/0/0；证明此快照未走gate。
2. 解除LSM，将正在运行的同一dev/inode可执行文件改名并保存独立readlink。原client FD执行AF_UNSPEC，返回0且SO_ERROR0、TCP_CLOSE、cookie不变，状态不变。关闭旧accepted/listener并确认目标没有listener。
3. 原FD重新connect必须得到ECONNREFUSED(111)，累计1/1/1/0/0；原记录只允许consumed从0变1，其余原始字节保持，包括旧路径与捕获时间。独立路径观察须与旧路径不同。
4. 新socket对照必须EPERM(1)、无记录，最终1/2/1/1/0。

记录各步原字节/时序、挂接/解除/销毁信息、FD与文件恢复，严格验证导出hash、内核与夹具pin、内核日志和独立virtio证据通道。无法满足预期时保存失败，不修改预登记结果使原run通过。

不声称代理转发、exec扩展、所有LSM遗漏路径或完整身份认证；无正式G01新增样本。该负例用于决定后续捕获连续性和新鲜性方案，不能仅增加一次CAS后就放行名称/路径规则。
