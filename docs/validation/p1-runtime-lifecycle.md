# T032：运行意图、停止、重新启动与有界重启

2026-10-27。T032 已完成 Linux/amd64 基础模式的有限模块验收。显式停止先保存
stopped 意图，再核验并终止原核心；重新启动复用最后已提交配置，生成新的运行身份。
经显式授权的重启监视器只允许一次重启，服务关闭或重开不会自动恢复该授权。
这些是后端模块能力，服务和命令接线由 T033 完成，产品故障矩阵由 T034 验收。

行为定义见[运行生命周期契约](../../specs/001-hoopa-proxy-manager/contracts/runtime-lifecycle-v1.md)，
机器索引见p1-runtime-lifecycle-validation.json（本地原件 `../../experiments/gost-mihomo/p1-runtime-lifecycle-validation.json`，不随Git上传）。

## 实现与不变量

- `BasicApplier.Stop` 在同一 Coordinator 控制锁内完成接纳、stopped 意图持久化、
  新鲜身份核验、原 pidfd TERM/退出观察，以及 publishing → active → committed。
  保留原 generation 和配置内容，已提交运行意图产生新 revision。
- `BasicApplier.Start` 必须找到已提交 stop 的完整退出证明；空状态或不明状态不能启动。
  它读取原不可变 bundle，使用新工作目录、nonce 和 RunID，核验闭态、登记、激活、
  核验开放后发布。不重新下载或调用材料 Source。
- 显式 Recover 可以继续尚有原活核证明的 stop，也能接管已经产生、尚未登记的
  closed runtime.start 实例；恢复不再 spawn。运行意图已持久但 TargetRunID 未持久、
  出生原件缺失或退出事实未持久时保持 degraded，不能凭 PID 消失推断安全。
- `OwnedRun.WaitExited` 只等待原强身份句柄；它不发信号、重开 PID、回收子进程或
  决定重启。退出观察与子进程 Wait 结果分别记录。
- `AuthorizeRestart` 是内部显式授权入口。每个 Coordinator 生命周期最多授予一次
  watch，值复制、Close、stop/start 或预算耗尽均不重置。watch 在控制锁外等待原
  pidfd；退出后退避一次 100 ms，重新核验 active 与全部历史，再消费一次预算。
  重启保留 revision/generation，生成新的运行身份；后继再次退出只报告预算耗尽。
- 同请求重放先于当前 revision 校验。生命周期操作正文暂不允许压缩，因为后续
  Start/Recover 需要核对原始证据。普通 apply 的终态压缩规则保持原有范围。

## 实际固定核心场景

三场使用同一固定 basic 核心 SHA256
`2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`，
宿主 Linux/amd64、UID 1002；没有 VM 或 BPF。材料是可信预编译测试输入，不能代替
通用订阅/规则编译器。合计三场、六次核心 exec、九次授权本地 HTTP echo；下面两批
证据保留各自输入版本，不合并成一次最终版本的矩阵。

| 场景 | 实际观察 |
| --- | --- |
| Stop/Start/Recover，`actual-02` | 三核 PID 2301742 → 2302807 → 2303910；公开 Stop/Start 先正常完成；随后在 stop 意图持久后中断，公开 Recover 完成原停止；再在 start 已 spawn、尚未登记时中断，Recover 核验并开放同一个闭态核心，零额外启动。四次 echo，材料 Source 仅首启调用一次 |
| 一次重启预算，`restart/actual-01` | 两核 PID 2350093 → 2350534；原 pidfd SIGKILL 后一次 Attempt 产生新 RunID，revision/generation 不变；后继再次被原 pidfd SIGKILL，watch 副本返回预算耗尽，状态树不变、没有新增运行。三次 echo |
| Close 与 Stop 优先，同批第二场 | 核心 PID 2351102；Close 唤醒已进入的等待，原核心继续转发；同 Coordinator 不能再授权。关闭并重开 writer 没有自动启动；新显式授权后公开 Stop 完成，Attempt 返回冲突且不改变 stopped revision。两次 echo |

六个核心均确认原 pidfd 退出与 `/proc` 消失；不将进程消失冒充单独采集到的
`basicRuntime` 子进程 Wait 退出码。SIGKILL 只作用于测试自己已核验的原 pidfd。
Stop/Start 包保存四个边界的 55 份 active/operation/run 原件，restart 包保存 25 份。
bundle/config/log 的字节核验在测试内完成，未另存的原始字节不冒称已归档。

Stop/Start 的实际样本早于最后的单次授权及孤立运行来源防护修订；重启两场与下列
最终回归使用修订后源码。旧样本不改标为新源码重跑结果。实际管理器 OS 死亡仍引用
[T031 的独立 SIGKILL 样本](p1-manager-crash-recovery.md)，本轮未重复该矩阵。

## 验证与审查

| 检查 | 结果与分母 |
| --- | --- |
| 原句柄退出原语 | 4 顶层、3 子测试，7 pass、0 fail/skip；其中两个真实 Go 子进程、一个空 helper，其他为守卫与汇总节点。无 Mihomo 样本 |
| Stop/Start 定向测试 | 5 顶层、30 子测试，35 pass；错误原件路径修正后另有 1 顶层、4 子测试复验，不把重叠节点相加 |
| Stop/Start 实际 | 2 顶层 pass＝1 实际场景＋1 空 dispatch，0 fail/skip |
| Restart 定向测试 | 2 顶层、7 子测试，9 pass、0 fail/skip |
| Restart 实际 | 3 顶层 pass＝2 实际场景＋1 空 dispatch，0 fail/skip |
| 最终稳定源码回归 | `go test -race ./internal/control ./internal/runtime`，排除已单列实际核心场景；480 pass、0 fail/skip，76 顶层/404 子测试，control 396、runtime 84，55.43 s，输入摘要未变 |
| 最终静态及格式检查 | 两包 `go vet` 退出 0；46 个 Go 文件 gofmt 与空白检查通过 |

最终回归包含原句柄 Go 子进程用例与空 dispatch 节点；480 不是独立真实故障场景数。
此前 T031 四包 1037-pass 属于此前冻结版本，本次没有将它作为新增生命周期代码的
回归结果，也未与本次分母相加。

root 负责状态机、锁、发布/恢复顺序及最终验收。executor 实际使用 gpt-6.1-sol /
medium 实现并运行限定测试；scout 实际使用 gpt-6-luna / high 独立核对稳定源码和
冻结引用，没有子代理再派生。阶段审查发现的生命周期正文压缩与重复授予重启预算
问题均已修复；最终源码审查与各批原件核验独立保留。

历史失败不删除：两次 stop/start 编译失败、首轮测试分类/种子状态错误及并发输入
变化、实际首轮相对核心路径错误（零核心 exec），以及 restart 的编译准备失败均
保存在私有包。原件损坏测试最初使用错误文件名，只证明未知文件拒绝；修正为真实
权威文件名后单独复验，最终 480-pass 回归也包含修正版。

## 后续边界

T033 尚需 CLI、管理服务、control-only 和 service.stop 接线及退出协作；本模块没有
默认启用后台守护、自动授权、系统服务、TUN 或全局代理。service.stop 必须先取消
worker，在控制锁外关闭 watch，再释放 Coordinator，不能通过退出顺手停止核心。

config.apply 的 closed-start 恢复仍遵循既有保守策略；本次 runtime.start 的正例不能
扩展为所有 Apply 崩溃窗口通过。T034 完整 F01–F14/F18/C01、服务会话/休眠、原生
断电和跨平台尚待各自验收。P0、P1 产品及完整项目均未完成。
