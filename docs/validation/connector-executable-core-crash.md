# v4活动流崩溃、重开与恢复边界

2026-10-24，[新预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-core-crash-v1.md)在001–013核心、executable-online组合对象和独立隔离VM上通过。来源旧流保留12/12检查点及未知尾部，新核心完成33/33精确终末；独立测试进一步验证内部writer产生稳定gap且原事件完整重放。两类证据在机器记录（本地原件 `../../experiments/sourceidentity/executable-core-crash-validation.json`，不随Git上传）中分别保存。

## 实际VM

固定核心SHA256 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`，镜像 `779d308168efed58da8981bb0069c6de36dcd459459988dd4fd1b77b2fb1193c`。105项离线准备后运行一次VM，canonical extractor和checker均退出0；31份导出逐项核对，独立scout复核符合。

| 事实 | 旧核心/旧流 | 新核心/新流 |
| --- | --- | --- |
| 核心PID与退出 | PID77，运行器对自有子进程SIGKILL，wait=-9 | PID88，正常SIGTERM退出0 |
| 原connector | PID/TID85、cookie2 | PID/TID95、cookie4 |
| 入口与实际线路 | HTTP→node:a，ready 11ms | SOCKS5→node:b，ready 32ms |
| 流量 | 完整12/12检查点，无closed | closed精确33/33 |
| 只读恢复 | pending、final=null、tail=null | closed、final=33/33、tail=0 |
| 实际kernel对象 | 9map、6program、5link | 全新9map、6program、5link |

先确认旧活动流有完整且摘要正确的12/12检查点，再终止核心。旧客户端继续操作时确认断流并退出1，正常超时不能代替这项证据。客户端被reap、source叶为空后，新的bootstrap使用新捕获generation及全新资源，重开同一来源目录。两代20FD/CLOEXEC、原PID/start/socket/exe证据与各自来源epoch一致。

两条opened均携带296B原始executable记录，exe为`/bin/python3`、dev2/inode8。两代call token都为1，分别绑定不同捕获generation，不能跨世代拿调用编号配对。旧manifest和frames重开前后逐字相同；两个epoch共13帧，旧未知尾部没有被填成零，独立代理/目标数据按各自token守恒。

## 独立内部恢复测试

[测试专用overlay](../../experiments/mihomo-events/probes/imported_v4_executable_crash_test.go.txt)在固定0013源码上通过一次race测试。输入明确指向本次VM的原始来源，先核对31份导出，再只在`TempDir`私有副本上调用实际`openSource`；不修改VM文件、生产源码树或二进制。

纯只读阶段不提供writer结束证据，旧流仍pending/null。随后三次重开由核心持排他writer锁、完整扫描并同步，旧流产生`epoch_ended_without_final` gap；12/12检查点保留，final/tail仍null，三次reconciliation ID及fact摘要一致。新流保持closed/33/33/tail0，不增加gap或重复终末。直接flock冲突返回EWOULDBLOCK，同样有效参数的第二个writer被拒绝；两份原帧重放逐字相同，包含原connector和executable字段。

本次实际VM接口没有导出内部恢复报告。上述六份writer报告来自测试副本上的三次重开，不能写成VM API已经返回gap。来源恢复事实的生产传输与持久化仍由后续任务实现。

## 适用范围

旧v3崩溃运行及其overlay保持原记录，新制品没有继承旧hash的通过结果。VM无网卡、无宿主共享；仅终止自有核心，不修改宿主BPF/cgroup/sysctl。此处不是管理器退出继续转发、断电持久性、自动服务恢复、长期map回收或跨平台保证；活动文件完整可见也不独立证明fsync。

T132/T137仍未完成，正式identity/name/path为false/unverified，G01新增0、原0/100和P0/P1门槛保持。后续继续固定hook操作边界、并发/早退及完整身份验收。
