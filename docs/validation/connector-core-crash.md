# 活动来源崩溃、重开与缺口回归

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-crash-v1.md)完成一次真实隔离 VM 实验及一次独立的核心恢复测试。候选仍为 001–009、SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`。完整摘要、输入/输出摘要及边界见机器记录（本地原件 `../../experiments/sourceidentity/core-crash-validation.json`，不随Git上传）。

## 真实 VM 的可见事实

私有运行 `private/t132-core-crash/vm-active-core-sigkill-mc78s9wp` 使用既有无网卡、无宿主共享的 Linux 6.18.54.1/KVM 环境；60 项离线工具断言先通过，再运行新镜像。未修改宿主 BPF、cgroup 或系统代理。

| 阶段 | 观察与判据 |
| --- | --- |
| 旧 HTTP 流 | connector PID 81、cookie 2，实际 node:a；读到完整且摘要正确的 12/12 字节检查点，保持连接，无 closed |
| 强制终止核心 | 仅终止运行器持有的核心子进程，wait 返回 -9；旧客户端后续操作确认断流，不能用任意超时充当断流证据 |
| 新 SOCKS5 流 | connector PID 92、cookie 4，实际 node:b；新核心、新 source epoch、新 capture generation，收发及终末均为 33/33 字节 |
| 捕获资源 | 两个 generation 在启动前分别登记；两代 map ID 为 1–6、7–12，link ID 为 1–2、3–4；配置 generation 可相同 |
| 原始材料 | 两来源的原 manifest/frames 重开后保持逐字相同；30 份导出文件摘要再次核对一致 |
| 只读恢复 | 旧流 pending，最后可信检查点 12/12，final 与未知尾部均为 null；新流 closed、final 33/33 |

读到活动文件的完整帧是可见性证据，不独立证明该帧已 fsync，更不是断电持久性试验。核心崩溃导致连接断开也不替代 T136 的“管理器退出而转发继续”验收。

## 核心内部恢复的独立验证

当前受保护接口没有导出 `scanPrevious` 的内部恢复报告，不能把上表的只读 pending 宣称为实际 API 已返回 gap。新增[测试专用 overlay 源码](../../experiments/mihomo-events/probes/imported_v3_crash_test.go.txt)，只将真实 VM 的两份来源复制到测试私有目录，调用固定核心的实际 `openSource`；它持独占 writer 锁、完整扫描和同步后，自行生成 writer 结束证据，测试不构造该证据。

一个顶层测试在 race 检查下通过，0 skip，实际连续重开三次，取得六份原来源报告。每次都验证：

- 另一个独立打开的锁 FD 得到 `EWOULDBLOCK`，第二个 writer 无法打开。
- 旧流为 `gap / epoch_ended_without_final`，保留原 12/12 检查点；没有伪造 final，尾部仍为 null。
- 缺口 reconciliation ID 与事实摘要在三次重开间稳定，不随新 writer 的 epoch 变化。
- 已结束流仍是精确 33/33 final，没有新增缺口或重复终末。
- 原始文件和重放帧逐字保持，包含原 connector、事件 ID、序号与整数计数。

首轮记录为 `private/t132-core-recovery-probe/test-run-1.jsonl` 和 `result.json`。独立审查后补强闭合流 tail=0、gap dimensions/reason/事实 ID 为空的断言，第二次定向 race 测试仍为 1 顶层通过、0 skip，记录为 `test-run-2.jsonl` / `result-run2.json`；首轮源码和日志保留，不把两次重复执行算成两个独立测试。实际 VM 原只读报告亦独立核对这些字段正确，旧冻结 checker 不覆盖。

测试使用 Go `-overlay` 映射 `.go.txt` 到固定核心包，不写入核心源树、不重建候选二进制。验证后完整 1155 项目录/文件/模式/摘要与审查树相同，961 个文件的源树摘要仍为 `a00b376e3965a736eac8db0fabbc8319b19fa7a3bc595920de606d8528f0d65f`。

这是固定核心在真实原帧副本上的恢复证据；生产报告传输及账本提交仍由 T138/T073 实现验收。未将只读 status/doctor 改为封存或修复操作。

## 状态

本批实际 VM 和恢复测试均通过，未抹除历史失败。T132 继续验证同名父子独立连接、PID/tuple 复用、并发、namespace、覆盖连续性和名称一致性；T137 仍依赖其结果。任务保持 139 项/21 完成，正式 G01 新样本 0、旧 0/100 保留，P0/P1 未放行。6.6、其他平台、真实 SSH/注销/休眠及完整性能预算未验证。
