# T034 固定双入口转换与实际连续性验收

2026-10-27。T034 的一个有限实施结果：两套完整固定配置已经转换为真实 `BasicMaterial`，经公开 `BasicApplier` 和实际 Hoopa CLI/service 执行，独立上游证明两个入口的实际选路。**T034 整体仍未完成，任务计数仍为 47/140。** 本记录不替代 `tests/integration` 中的完整故障矩阵，不提高平台、计量或通用配置编译能力声明。

## 固定输入与实现

新增 `internal/testsupport/p1fixture/`，所有文件仅在 `linux && amd64 && p1integration` 下构建。它只接受 [basic-verified-bundle.json](../../testdata/runtime/basic-verified-bundle.json) 中的 `app-first`、`domain-first`：分别读取完整原始配置和独立预期 snapshot，逐项校验文件大小、SHA256、路径和权限，返回深拷贝。没有从 `observed_snapshot` 生成预期，没有删除入口、认证、规则、组或节点，也没有修改固定端口。

| 输入 | 配置字节数 | 配置 SHA256 | 双入口预期实际节点 |
| --- | ---: | --- | --- |
| app-first | 1248 | `749b10ecfa886acd847745d702b85aa5bf14a29304bf63ab8151ee6562a37af3` | node A |
| domain-first | 1248 | `b58171f01bd53d33a840031cf3f869ba30179a6f75b15227954450d2ed814b2f` | node B |

固定核心 SHA256 是 `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。Loader 绑定批准的能力声明；实际场景另行打开、核对真正的可执行文件。`CurrentInputs` 重新加载固定原件、验证可执行文件，并从当前只读 active 指针计算 start/replace 影响，不回显 Plan 的输入或影响作为“观察”。

测试 Intent 明确标为 `hoopa-test-finite-basic-v1`，只是精确有限输入的引用。生产服务仍没有通用配置编译器，依旧拒绝尚未提供的 config.plan/apply 能力。没有把测试 Source 接入发行 CLI。

## 实际执行与独立观察

实际场景在 [p1_finite_core_test.go](../../tests/integration/p1_finite_core_test.go)，网络见证在 [p1_fixture_network_test.go](../../tests/integration/p1_fixture_network_test.go)。依次执行：

1. 公开 Apply 启动 app-first，再公开 Stop，明确准备一个已提交且证明停止的快照。这两个操作及其第一核心均保留在证据中。
2. 启动实际 `hoopa service run` 子进程；经真实 `hoopa api v1 runtime.start` 从停止状态启动第二核心，生成新 RunID/revision。
3. entry-a 建立长连接，entry-b 建立新连接，均实际经过 node A。
4. 对自己创建的管理器子进程发 SIGKILL，等待确认其确实被该信号终止。原长连接再次收发成功，新连接也成功；独立 pidfd、完整进程身份和已连接控制器核验仍指向同一核心。
5. 离线 status 保留 service/collector unavailable、core unknown。管理器死亡和离线查询没有改变完整配置/持久状态文件清单。
6. 重新启动实际管理服务，单纯重开不修改持久权威状态。随后通过新请求显式 `operation.recover` 指定原 runtime.start 操作，保留原核心、active/revision 和原 start 收据；长连接和新连接仍成功。
7. `service.stop` 关闭管理服务，长连接继续转发。然后公开 Apply 将配置替换为 domain-first；旧核心确实退出，旧长连接中断，两个新入口连接均实际经过 node B。
8. 公开 Stop 停止最后核心。退出核验后，只对管理器死亡留下的精确孤儿 PID 做有界 Wait4 回收；三个核心的原 pidfd 均观察到退出且 `/proc` 条目消失。

每次回显使用独立随机 4096 字节 payload。实际接受 CONNECT 的上游监听器记录 node、目标、连接源地址、payload/echo SHA256 和原件；客户端用同一 payload 的 SHA 关联证据。上游把数据送到独立 TCP echo origin，核对返回后才记录见证并回送。node 来自实际到达的上游监听器，不从预期规则或控制器选择反推。

这 4096 字节是测试 payload；另有 4 字节测试帧头及代理协议开销。它们不是 Hoopa 应用账本、计量完整性或计费字节的验收。长连接复用同一 socket，换配置后的失败不伪装为自动重连后的成功。

## 验证结果与版本边界

- Loader 普通测试与 race：各 **7 个顶层、20 个子项，27 pass，0 fail/skip**；普通 0.051s、race 1.232s，vet 通过。17 项工具/源码/原件 pin 复核一致。
- 最终普通实际运行 `actual-04`：**2 个顶层 pass，0 fail/skip**，其中一个是无启动环境的空 launcher dispatch，另一个才是完整实际场景；实际场景 18.81s。
- `actual-04` 共 **3 个核心运行、2 次 Source、9 次独立 payload 回显、1 次管理器 SIGKILL**。两个入口在规则替换前后分别走 A、B；没有新增重复核心。最终三个核心都不需要测试清理信号，孤儿 Wait4 状态为 0。
- 最终 `race-04`：同一场景 **2 个顶层 pass，0 fail/skip**，实际场景 25.34s；同样 3 个核心、2 次 Source、9 次回显及完整退出/回收。只有 integration 测试监督进程、其中的 BasicApplier 和网络辅助代码启用 race；实际 CLI 与固定核心是普通构建。两轮分列，不把重复运行合成额外矩阵样本。
- 最终 tagged vet 同时检查 integration 和 loader，退出 0。第一轮 vet 对测试网络结构的 String/GoString 值接收器报告复制互斥锁，改为指针接收器后复验；原失败记录保留。
- `actual-01`、`actual-02`、`actual-03` 及 `race-03` 是同一场景的阶段通过记录，第二版补充完整清理原件，第四版固定最终格式化源码；不把重复执行加成新的矩阵覆盖。系统 `python` 为旧解释器造成的首次脚本语法错误发生在任何实验进程启动之前，实际脚本使用 Python 3。

实际运行前冻结源码和固定核心字节；运行后逐项核对未变。私有运行目录还保存构建出的 CLI/测试二进制、请求回执、前后持久文件、上游见证和清理记录。凭据、二进制与原始状态留在忽略目录，公共仓库只存源码及摘要索引。机器索引见 p1-finite-dual-entry-validation.json（本地原件 `../../experiments/gost-mihomo/p1-finite-dual-entry-validation.json`，不随Git上传）。

## 剩余工作

已完成：固定双入口转换、实际 start/管理器死亡/终态显式恢复/配置替换的有限场景及独立线路证据。

进行中：完整 `newProductHarness` 接线，真实 launch/signal/workflow boundary 的事件时刻记录，以及跨管理器 SIGKILL 保留的故障命中原件。当前 factory 仍明确失败，不返回模拟成功。必须先提供进程外同步见证，再执行每个故障切点；不能从最终 journal 重建一段“曾发生”的事件轨迹。

失败待决策：本轮没有未裁决问题或缺少的用户授权；已修复的测试辅助代码问题及旧失败保留。

环境不足未验证：本轮是宿主 Linux/amd64、固定 loopback TCP；没有 SSH 断开、登录 shell 退出、系统休眠、真实断电、其他平台或资源预算的新验收。受限进程来源、UDP/QUIC、完整账本和质量指标不在此场景。完整 T034/F01–F18 仍需逐项验收，P1 及整体项目未完成。

root 负责 live impact、进程身份、恢复/替换顺序和最终接受；executor 按角色实际使用 gpt-6.1-sol / medium 实现有限 loader 并测试；scout 按角色实际使用 gpt-6-luna / high 做只读拓扑、执行接缝和独立证据复核。没有子代理再次派生。
