# T030 支撑能力：运行枚举与经核验终止

2026-10-27，root 有限验收。T030 仍未完成。机器索引为
p1-apply-support-validation.json（本地原件 `../../experiments/gost-mihomo/p1-apply-support-validation.json`，不随Git上传）。

`Store.ListRunIDs` 只读枚举最多 1,024 个持久运行意图，校验私有目录、文件原件与目录名绑定。
未知名、损坏、超限或 staging 都不返回部分清单，也不清理记录。它不会重建启动许可；
缺少 birth 不能解释为进程已经退出。只读句柄只能观察可见字节，不能代替持久化屏障。

`Ownership.Terminate` 要求调用方持有协调器锁，且已经持久记录停止步骤。它读取本操作
注册，重新验证原 pidfd 的 argv、所连接的 controller socket 与完整配置，再重读注册
及摘要、核对 argv，最后向原 pidfd 发送一次 SIGTERM 并等待同一句柄观察到退出。
不退化为数字 PID、不重试发送、不升级 KILL，也不承担子进程 reap。任何错误都不能
解释为“没有发过信号”。当前仅支持 closed/open，draining 仍拒绝。

验收使用任务专用私有目录和真实普通宿主用户身份，固定 Go 1.27.3、离线依赖。
run inventory 所在 config 包 race 为 52 顶层、349 子测试，共 401 pass；runtime 包
race 为 17 顶层、40 子测试，共 57 pass，其中 2 个空 helper 单列。两包均无失败或
跳过，vet 通过。枚举测试有实际 1,024/1,025 项边界与父目录同步失败后的 poisoned
writer；终止测试使用 3 个一次性 Go 子进程，分别验证原 pidfd 退出和独立 child Wait。
这些子进程不是 Mihomo，不构成业务替换证据。

审查修正了两处测试见证：有效注册更换保留规范序列化并额外读回成功后才测试拒绝；
目录符号链接用例把原目录移到 runs 之外，避免先被未知目录名拒绝。旧通过日志保留
为有限历史，不拿它们代替修正后的证据。额外换行仅证明损坏拒绝。

主代理负责流程与恢复语义、运行枚举实现及验收；executor 角色实际配置为
gpt-6.1-sol / medium，负责终止实现和测试；scout 为 gpt-6-luna / high，负责只读调用链
及冻结引用核对。终止的 83 个冻结引用核对一致。上述 config 验收先于随后新增的
operation admission 预检；后者和完整 apply 需要自己的检查，不能借用本次 401 pass。

未验证：不响应 TERM 的核心、发送信号失败、完整替换与回滚、产品进程恢复、CLI 分派。
T031/T033/T034 的这些工作继续保留未完成状态。
