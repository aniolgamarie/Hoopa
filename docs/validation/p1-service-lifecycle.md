# T033：实际管理服务与 CLI 的有限验收

2026-10-27。Linux/amd64 的 Cobra CLI、独立管理服务、发现/就绪、service.stop、
普通 start 接管和单次重启接线已完成有限验收。T034 完整生命周期故障矩阵及 P1
其余模块仍未完成；这不是整个项目完成声明。

契约见[管理服务生命周期](../../specs/001-hoopa-proxy-manager/contracts/service-lifecycle-v1.md)、
[传输](../../specs/001-hoopa-proxy-manager/contracts/service-transport-v1.md)和
[数据 Schema](../../specs/001-hoopa-proxy-manager/contracts/service-lifecycle-v1.schema.json)。
机器索引（本地原件 `../../experiments/gost-mihomo/p1-service-lifecycle-validation.json`，不随Git上传）保留执行回执、
冻结输入、原件摘要和此前失败位置。程序与测试二进制仅存私有构建目录，不加入源码仓库。

## 已实现范围

- `start`、`start --control-only`、`service run/stop`、`stop --accept-disruption`、
  `status`、`operation get/recover` 和对应六个 JSON v1 操作。Cobra 1.10.2、pflag
  1.0.9、mousetrap 1.1.0 按既有已校验提案固定，go.sum 保留。
- 实际 writer 持锁后发布私有实例端点；SO_PEERCRED 和实例握手后才交换有界帧。
  原服务失联不能授权删除 socket，重开使用新实例。两个并发显式前端收敛到一个 writer。
- 只有显式 start 可以后台启动管理服务。就绪管道、日志、环境、超时、子进程清理有界；
  就绪后的服务独立于前端。普通 start 没有已提交快照时失败，不假装已经配置代理。
- service.stop 持久记录原实例并关闭请求接纳，保留发起连接发送收据；完成收据不是
  OS 已退出证明。旧请求在线或离线均精确重放，不停止新实例、不停止核心。
  六个持久切点的中断 stop 可由不同实例的新 writer 显式恢复；旧缺失身份不补造。
- 查询只读，未完成核心操作保持 unknown。operation.get 只提供脱敏历史索引/摘要，
  pending 最多 32 条并声明总数和截断；70 条最长引用的合成场景实际响应 29,475 B，
  小于 65,536 B，经过真实帧传输验证。
- 计量/采集当前 unavailable，通用编译器明确拒绝，未提供 P2 空成功命令。

## 真实核心与产品命令

固定 Mihomo basic 二进制 SHA256：
`2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。
UID 1002，隔离 0700 临时目录、本地回显目标，无 VM/BPF、外部订阅或外部流量。
独立完整 fixture 只用于预先 Apply；产品 CLI 不得到 fixture 编译器。

| 场景 | 实际结果 |
| --- | --- |
| actual-05，真实 CLI 连续性 | 2 个核心，PID 1073961 / 1076076，6 次授权 HTTP echo，材料 Source 一次 |
| 运行中普通 start | 完整原身份不变，无重复 run；CLI 前端退出后继续转发 |
| 服务停止、重开和旧 stop 重放 | 核心 active/bundle/run 原件不变，原收据字节一致，新服务保持可用，原核心继续转发 |
| 核心 stop 后 start | 原 pidfd 证实退出，新核心通过产品专用 launcher 启动，运行记录恰好增至两个 |
| restart-01，实际前台 service --restart-once | 2 个核心，PID 1116789 / 1118056，3 次授权 echo，Source 一次 |
| 单次重启预算 | 就绪/control-only 均未授予预算；新普通 start 后观察原 pidfd，首次任务限定 SIGKILL 后启动一个后继；后继再次退出只观测，不再启动；revision/generation 不变 |
| worker 收尾 | 第二次退出后服务显示原实例已退出，显式 service.stop 正常结束前台服务，无锁等待挂住 |

两场共 **4 个核心、9 次 echo**；每场单独 2 个顶层 pass＝1 个实际场景＋1 个空 launcher
父分派，0 fail/skip。实际两场未加 race；后述回归中的实际服务子进程带 race，不能
把它等同于这两场核心已做 race。实际连续性版本在最终 watch 退出原因投影前；新增
`runtime_exited` 投影只在 restart-01 的最终版本中实测。冻结源码分别保留，不混写版本。

离线运行状态刻意显示 unknown / activation_requires_management：现有完整激活证明
要求管理 writer，查询不会为了得到“绿色”状态获取写入权。此时核心存活由测试独立
原 pidfd、完整身份和真实 echo 证明；不把离线状态本身当作转发证明。停止证据/未完成
记录仍可只读查询。合成与实际结果都不承诺整个状态查询是跨进程原子快照。

## 最终检查与保留的失败

| 检查 | 范围与结果 |
| --- | --- |
| 最终受影响包 race | control/service/cli，633 pass、0 fail、1 skip，114 顶层/520 子项，84.55 s，冻结输入未变 |
| 唯一测试跳过 | TestBasicMaterialActualApprovedBinary，此回归未传可选固定核心变量；不是通过。cmd/hoopa 另为无测试文件包，不计为测试项 |
| 实际服务子进程 | CLI 测试在 race 回归时用 -race 构建子程序，包含真实就绪、退出、并发前端和跨实例重放 |
| 最终 vet | control/service/cli/cmd，退出 0，2.47 s，冻结输入未变 |
| 定向 service.stop | 12 顶层，包括六持久切点、原实例证据、旧 stop 重放、quiesced 拒绝、恢复不处理其他 pending、首 Apply 和压缩保护；合成 callback，不冒称核心/OS 退出 |
| 定向 API | 8 顶层，实际 writer 保持持锁时只读查询/重放仍成功且文件不变；损坏发现拒绝回退；错误帧后服务仍可用；结果已包含在最终回归中，不重复相加 |
| Schema | 3 个人工形状 fixture、15 条实际 CLI 收据、4 条实际重启状态，共 22 个正例；3 个形状反例拒绝；jsonschema 4.26.0 |
| 格式与空白 | 30 个新增/受影响 Go 文件，gofmt 和空白检查通过 |
| 平台 | Windows/amd64 仅 explicit-unsupported stub 交叉编译；没有 Windows/macOS 运行支持声明 |

原 actual-01 因独立测试二进制工作目录错误而未读到 fixture；actual-02 错把离线查询
预期为完整激活证明；actual-03 的保护树错误包含服务发现/日志；actual-04 在已有
停止来源校验处遇到只读边界，尚未投影具体原因，且并行测试文件写入导致输入变更。
这些失败全部保留，分别修正夹具或未知原因后由 actual-05 通过，不改写旧记录。

临时系统盘 ENOSPC 曾阻止一个未执行检查，随后仅移动/删除本任务可重建缓存，
保留工具链、依赖缓存和原始回执。编译缓存转到工作区；实际权限测试仍在私有 /tmp。
没有降低目录/文件归属检查，也没有把沙箱 UID 视图的失败算成通过。

## 边界与后续

已完成：本任务服务/CLI 接线及上述有限场景。进行中：T034 的产品 harness 和完整
生命周期故障矩阵。失败待决策：本任务范围无未解决项，历史失败按原样保存。
环境不足未验证：实际 SSH/logout、休眠恢复、macOS/Windows 行为及整套性能预算。
这些不会被“编译成功”替代，也不自动阻塞其他独立实现。

root 负责事务/锁/恢复取舍与最终验收；executor 实际 gpt-6.1-sol / medium 实现 CLI、
端点和限定测试，scout 实际 gpt-6-luna / high 独立核对稳定源码/证据，没有再派生。
T034 仍未勾选，P0 整体与 P1 产品未完成；旧 G01 0/100 及后续历史失败记录不变。
