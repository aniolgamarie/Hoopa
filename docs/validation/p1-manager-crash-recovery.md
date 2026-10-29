# T031：真实管理器 SIGKILL 后的有限恢复验收

2026-10-27。最终 `manager-crash-04` 在独立管理器 OS 进程持有控制锁、已发布
active 而原操作尚未提交终态时发送 SIGKILL。原核心继续转发；另一进程中新建的
Coordinator 取得 writer 锁，以公开 `BasicApplier.Recover` 重新核验同一实例，完成
原操作及独立恢复调用，没有重复启动核心。此项补足此前仅 Close/reopen 的证据边界。

测试见 [recover_crash_core_linux_test.go](../../internal/control/recover_crash_core_linux_test.go)，
机器索引见 p1-manager-crash-recovery-validation.json（本地原件 `../../experiments/gost-mihomo/p1-manager-crash-recovery-validation.json`，不随Git上传）。
自动回滚及其他恢复证据分别见[自动回滚](p1-basic-rollback.md)、[显式恢复](p1-explicit-recovery.md)。

## 最终实际样本

| 项目 | 观察结果 |
| --- | --- |
| 平台/核心 | Linux/amd64，固定 basic build `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb` |
| 测试父进程 | PID 1959979；恢复 Coordinator 在该进程中新建 |
| 原管理器 | 独立子进程 PID 1959989；原 `exec.Cmd.Process.Kill`，`Process.Wait` 确认 signal 9 |
| 原核心 | PID 1959999，启动 tick 39455129；RunID `run:59764e0c30fe2aaf989de7dd40347e0e` |
| 转发 | SIGKILL 前、SIGKILL 后、Recover 后各一次授权本地 HTTP echo，共 3 次 |
| 控制锁 | 杀前第二 writer 被拒绝；杀后新 Coordinator 成功取得锁 |
| 恢复 | 新鲜 pidfd、完整出生身份、原注册、完整快照及精确激活回执核验；核心 PID/RunID 不变 |
| 持久结果 | 原 apply 从 publishing 到 committed；恢复调用独立 committed；原请求身份不变 |
| 重放 | 原 apply 与恢复请求均只返回存储结果；状态树不变、零新增 RunIntent |
| 回收 | 原核心 pidfd TERM/退出观察；收养后的 `Wait4(1959999)` 返回 exit 0，再确认 `/proc` 消失 |

原管理器执行生产接纳和应用执行器，以私有测试中断点停在 `active-published`，
尚未退出、释放 mutex 或 Store flock。恢复实例只得到路径与可信固定材料，未接收
测试父进程保留的核心句柄；该独立句柄用于真值核对与最终清理。

Linux subreaper 仅作用于当前测试父进程，原值 0，临时设为 1，回收后恢复 0。
它使测试能收养并回收管理器遗留的核心，不是 Hoopa 生产托管策略，也未修改系统
服务或 PID 1。`WaitExited` 与 `Wait4` 分开断言，前者不冒充回收退出码。

## 原件与版本

最终私有包保存 bootstrap、ready 消息、原管理器 `/proc/<PID>/stat`、实际使用摘要绑定的
run intent/birth/registration、恢复前后的 active 与原 apply 操作正文、恢复调用
接纳前不存在的观察及接纳后正文，以及管理器 stdout/stderr 和核心 launcher.log。
目录 0700、原件文件 0600；含固定测试认证材料的原件不发布到公开文档或 Git。
恢复前后 run 原件逐字一致；active 已在杀前发布，本次 Recover 未再改 active。

历史尝试保留，不合并为最终场景分母：

- `manager-crash-01` 在继承管道设置 deadline 时失败；未执行预定 SIGKILL 恢复。
  已启动核心 PID 1953234 经强身份核验、原 pidfd 退出观察和收养回收确认清理。
- `manager-crash-02` 的实际死亡/恢复通过，但未另存运行身份与操作原件；限于当时
  测试断言与日志证据，不能冒充后补的完整原件包。
- `manager-crash-03` 增加原件归档并通过。根审查随后发现父进程 `File.Fd()` 会将
  pipe 改回阻塞，削弱后续读取 deadline 的保证；该版本不作为最终超时边界证据。
- `manager-crash-04` 改用 `SyscallConn.Control` 观察 FD；启动管理器前，对同一
  ready pipe 实测 25 ms 无数据读取返回 deadline exceeded、0 字节，再清除 deadline。
  防护分支在尚未 spawn 时关闭唯一 writer 并收割读者，避免自测挂起。最终场景通过。

最终 race 为 3 个顶层 pass、0 子测试、0 fail/skip：**1 个实际场景与 2 个空 dispatch**，
不是 3 个独立故障场景；vet 退出 0，输入前后摘要一致，新增文件 gofmt/空白检查通过。
生产实现未因本次补验改变。此前四包 1037-pass 回归仍属于[原冻结版本](p1-explicit-recovery.md)，
本次只增加独立实际测试，两个分母分列。

root 负责恢复语义和最终验收；executor 实际 gpt-6.1-sol / medium 实现及运行测试，
scout 实际 gpt-6-luna / high 独立核对冻结源码、原件和证据；没有子代理再派生。

## 仍需完成的范围

这是一场固定 basic 核心、测试管理器的真实 OS 死亡样本，未经过产品 CLI、Unix
管理服务或完整集成适配器；没有覆盖所有应用/回滚持久边界，也没有证明存量长连接
在本次杀进程期间连续传输。SSH/logout/休眠、原生断电、跨平台及完整 F10–F14
矩阵仍按 T034 和平台阶段分别验收，不能用本场景替代。

T031 的完整旧态回滚、失败 degraded、独立恢复调用、已开放实例显式恢复与此项
管理器死亡补验构成有限模块验收。closed/身份缺失/未证退出窗口继续 degraded；
后续 T032 必须补运行意图、显式停止与受控重启，T033 接服务和 CLI，T034 执行产品
矩阵。P0、P1 产品和整个项目均未完成。
