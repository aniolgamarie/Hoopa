# T025 运行归属模块有限验证

2026-10-27。状态：root 已接受 T025 的 Linux/amd64 模块责任；对应任务勾选，产品阶段仍未完成。
机器记录：p1-runtime-ownership-validation.json（本地原件 `../../experiments/gost-mihomo/p1-runtime-ownership-validation.json`，不随Git上传）。
这是 Linux/amd64 库与存储接线验证，**不是 T026 真核心门控、T033 服务或 T034 产品生命周期验收，也不表示整个 P1 通过**。

## 已具备的模块责任

- `PrepareLaunch` 独立使用 CSPRNG 生成32字节 nonce、16字节 RunID，拒绝预填身份；配置 Generation 与 OperationID 保持调用者原值。只有 intent 已持久化才返回新票据，重读或重复 Prepare 不再授予 spawn 权利。
- launcher 读取继承目录与原 intent 摘要，采样自身真实 PID/UID/boot/start，将 birth 无覆盖发布并完成 Sync/close 后才返回；传入目录 FD 在成功或失败时都关闭。任一错误禁止继续 exec，birth 缺失不等于从未启动，也不能当作自动重试许可。
- 原 pidfd 强身份与完整 argv 校验、同一 TCP 连接反向 tuple/inode 与目标 FD 的归属核对先于 Inspector 发字节。Observe 只读；Register 必须重新观察，再持久化本次 operation 的登记。
- 恢复同一实例可用新 operation 登记。相同 operation 保留原登记的历史 gate，返回本次新观察；不会因为旧登记可读就绕过 Store 的只读或 poisoned writer 检查。
- 私有 Unix IPC 负责实际目录/端点/peer 边界、并发 Accept 的可取消串行 deadline 管理。调用者仍须持 Store writer lock；IPC 的同账户身份不等于 Mihomo 核心归属。

运行记录和权限边界见 [runtime-records-v1](../../specs/001-hoopa-proxy-manager/contracts/runtime-records-v1.md)、[ADR012](../adr/012-p1-file-store.md) 与 [ADR013](../adr/013-p1-runtime-ownership.md)。
此前[平台原语有限验收](p1-ownership-primitives.md)保留原范围和结论；本记录补足其后续存储、出生与归属库接线，不回写旧报告。

## 定向证据分母

每行仅指明该组件的最终原 run，不能相加重跑次数作为独立核心样本。顶层/子测试均为 Go 父进程节点口径，子节点包含中间 `t.Run`。

| 组件 | 有效顶层／空父 helper | 子测试节点 | 结果与范围 |
| --- | --- | --- | --- |
| 原 pidfd 进程原语 | 6／1 | 5 | 原平台记录，0失败/跳过 |
| 同连接 TCP owner 原语 | 5／1 | 11 | 原平台记录，0失败/跳过 |
| 私有 Unix IPC | 12／0 | 28 | 原平台记录，0失败/跳过 |
| argv／self birth 原语 | 5／0 | 10 | 原平台记录，0失败/跳过 |
| 运行记录纯类型 | 6／0 | 83 | 原平台记录，0失败/跳过 |
| run store／delegated birth | 9／0 | 28 | `host-race-03`，真实文件系统、合成 storage birth |
| runtime ownership／launcher | 5／1 | 14 | `host-race-02`，10个真实自有子进程 |
| CSPRNG launch preparation | 5／0 | 6 | `host-race-01`，存储 fixture，0子进程 |

后三组件对应包 vet 均通过，并有独立只读源码/receipt review，无阻塞发现；review 本身没有重新运行测试。各 receipt、review、freeze 与实际源码的长度/SHA256 均固定在机器记录中。

### 真实子进程接线与合成 controller

定向 runtime 原 run 实际启动、收集 Wait 的 **10个自有 Go 测试子进程**，每个先 Claim，再以同 PID `syscall.Exec` 同一固定测试 binary。launcher 核对出生目录 FD 已关闭，exec 后核对 FD3 不再指向原目录 inode，再用继承的 FD4 接受真实 loopback TCP。Go runtime 可能重用已经关闭的 FD 号码，验证针对原目录能力，不把号码重用误作泄露。child 原始日志和实际 PID 保留在原 stdout。

Inspector 经同一已证明 owner 的连接执行明确的合成 `OBS`/JSON 协议，读取 fixture 的配置绑定观察和 gate 文件。这证明库接线，**没有使用 HTTP/Mihomo 认证、真实路由配置或真正 closed-start/activate**。fixture 中 gate 改为 open 只是测试状态，不表示应用入口真实开放或关闭。

测试核对 Observe 前后 state bytes/hash/mtime 不变、closed 登记、关闭 OwnedRun 不杀 child、Store 写者重开后仍观察同 PID/birth 而不 spawn。相同 operation 的 fresh open 观察保留旧 closed 原件；新恢复 operation 登记同一 birth。这里重开的是 Store，父测试进程没有真实崩溃，不能代替管理器恢复矩阵。

missing birth、错误 birth PID/start、argv、nonce、binary SHA、controller config/gate 返回值均被拒绝且不登记、不发信号。出生/身份拒绝路径整个 child 生存期没有 controller accept 日志。只读 Store 的既有登记重试返回 `ErrReadOnly`，malformed 登记不修补；首次 requireClosed 对 open 拒绝。Claim 错 nonce/intent、重复或未完成 birth、取消均关闭继承 FD，错误路径不执行 exec。

存储故障矩阵是6个 intent、5个 registration、6个 birth 边界：发布前无 authority，发布后 Sync 不确定保留原件并阻止继续写；birth 临时名删除失败留下双链接，reader 拒绝。capacity 使用1024个合成占位项，不声称完成1024次 durable launch。故障由既有 checkpoint seam 注入，仍用真实文件系统，不等于真实断电或设备 fsync 故障验证。

## 稳定源码规定检查

固定 Go1.27.3、`GOTOOLCHAIN=local`、`GOPROXY=off`。一次覆盖 `internal/protocol/experiments/tests` 全公开源码范围，避免 raw `./...` 扫入 private 工具链副本。使用普通宿主 UID、可信 `/tmp` 下0700测试目录；只用默认 build tags，并移除继承的 opt-in 环境。

- gofmt 输出为空，go vet、普通 go test、go test -race、最后 `git diff --check` 均 exit0。
- 167份公开 Go 源码与 go.mod 共168份输入，逐命令和整轮前后 SHA 一致；实际 argv、耗时、环境、stdout/stderr 与输入原字节快照保留。
- 普通与 race **各**378顶层＋1446子节点＝1824个父 run 节点，1805 pass、19 skip、0 fail；两轮不相加作为独立能力分母。
- 4个空父 helper 单列：`TestProcessFixture`、`TestControllerFixture`、`TestOwnershipFixture`、`TestSourceHelper`。359个顶层 pass 中去除它们后为355个，不能将空入口当作 native 成功样本。
- 19个 skip 中17个缺少显式 opt-in／固定 candidate 或 acceptance 输入；2个旧 sourceidentity 用例因 owner-set 可见性不足，均保持未验证，不记成通过。
- 16个有测试包 pass；2个 `[no test files]` 包单列为 `cmd/controlprobe`、`cmd/preflight`，不混入19个 test skip。`p1integration` 产品定义没有启用，不能推导 adapter 可用。

这一轮普通/race 回归重复了上述 fixture；它们不是新增产品场景。没有重跑实际核心/VM opt-in 矩阵，新增真实 Mihomo／VM 样本均为0。

## 保留的失败与未完成边界

平台 fixture 权限、sandbox socket 拒绝、过长路径、runner 前置兼容与初次测试编译失败仍在各原包，保留当时源码 pins 和退出码。run-store 的 sandbox/首个 host 尝试因 `/data` 下 TMPDIR 祖先不安全而在事务前拒绝；root 后批准可信 `/tmp` 测试目录，新宿主尝试另列，未降低生产校验。历史重试不增加当前成功分母。

T026 仍须实现并实测真正核心 bootstrap、认证/完整配置和 closed-start/activate；T033 的产品服务与 IPC 业务协议、T034 的真实启动/恢复/注销/休眠矩阵仍未完成。熵源失败注入、birth 发布后精确取消交错、断电/设备持久性、真实异UID IPC及非Linux支持均未验证。同UID/root恶意替换、FD转交或exec竞态不在 ADR012/013 的隔离承诺内。

root 已完成稳定源码、独审及执行证据的模块验收；executor gpt-6.1-sol/medium 完成已定实现/检查与证据整理，scout gpt-6-luna/high 独立核对。根代理据此勾选 T025；没有新增核心样本或提升 P1 状态。
