# 可执行文件证据与原连接组合原型 v1

2026-10-24，运行前登记；T132 子步骤，非正式身份能力验收。本轮使用固定 6.18.54.1 隔离来宾、可信独占 loader、无网络设备/宿主共享；不改变宿主 BPF、cgroup 或 sysctl。

组合对象把[同调用编号检查](connector-executable-call-token-v1.md)嵌入原 online connect gate：目标筛选→writer admission→exe token/一次消费→原 connect 捕获→active/passive→核心原 accepted socket。296B exe 记录和112B connect 记录通过同 client cookie、TGID/TID 和 `leader_start ≤ exe_boot ≤ connect_boot` 绑定。connect 记录没有 token 字段；同 token 来自内核 gate 中 LSM 与当前 task storage 的比较及其执行顺序，不能称作两个导出记录直接比较 token。

资源使用严格 `hoopa-core-identity-handoff-v2`：9 maps（旧6+exe_records/exe_state/calls）、6 programs（旧3+call_enter/call_exit/capture_exe）、5 links，共20个不同FD。旧11FD v1不可套用。所有副本创建时CLOEXEC，全部复制完成才开启继承；same-PID exec之后核心在监听前核对对象/引用/访问模式/目标及初始状态，恢复CLOEXEC，失败按所有权只关闭一次。raw TP读取完整sys_enter/sys_exit目标名；LSM program/link BTF object与function一致，function39733，实际target BTF对象内容必须是6018902B、SHA256 `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9`。不能只检查函数数字或目标名长度。

exe_records初始为空、exe_state初始全零；calls只核对task-storage ABI，不冒称能够枚举为空。核心准入前后核对fault，按原accepted cookie→binding→client cookie读取原connect及exe证据；任意缺失/错配/未消费/非法路径拒绝并撤销当前owner。旧v3保留原字段约束，v4按[source-connector-v4](source-connector-v4.md)显式输出exe值副本，writer profile必须与adopted handoff一致，禁止版本降级丢字段。所有identity能力仍false/unverified，PROCESS规则继续拒绝。

本组合的故障策略是整个采集世代持续拒绝：一次exe admission拒绝（含缺钩子、旧快照、TFO）也增加共享capture_faults；与独立spike在局部拒绝后可继续的行为不同。root挂接下scope外目标连接导致同样拒绝属于原online继承限制。此轮不提供不可信同机多租户可用性，不能把不泄漏身份错配等同长期可用。恢复需要新资源世代，自动恢复/长期回收另验。

原writer gate仅覆盖connect/sockops；closed且active=0不能证明raw/LSM写入完全停止。Close先撤销准入，再drain该有限gate并释放全部link/program/map FD；不输出全采集稳定快照，也不以FD关闭代替RCU结束证明。

首轮实际判据：新组合对象verifier/load成功；新001–011核心接管20FD后启动，两个独立客户端以普通native connect分别经HTTP/SOCKS进入node:a/node:b，发送33B、SHUT_WR后收到33B和EOF；同flow opened保留原connector和独立exe oracle、ready实际线路、closed整数终末，旧v3能力不混入。每条流的独立客户端在connect前后记录真实 `/proc/self/exe` 字节、inode/dev、PID/start/cookie及单调时刻，oracle只供事后核验，不参与准入。两协议共2flow、期望13帧，原帧用实际v4解码/重放校验。核心SIGTERM退出0，来源结束完整，所有导出摘要核对。后续负例单独新登记，不能借旧对象的通过代替本组合结果。

失败保留原run/源码/hash和执行阶段；prepare/fake tests不算VM实测，受控Python客户端不是Codex正式100流。T132/T137、139项/21完成、正式新增0和旧0/100、P0/P1门槛均保持。

执行追加：首001–011实际VM因profile4未进入底层质量捕获开关而在原线路判据失败，原记录保留。后续0012仅修正该开关并增加进程初始化测试；使用新制品/镜像目录独立重跑，组合BPF及上述完整运行判据保持，不能用修改checker来接受缺失ID。
