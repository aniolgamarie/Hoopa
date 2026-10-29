# T033：普通 start 接管已运行核心的有限验证

2026-10-27。控制层新增 `BasicApplier.EnsureStarted`，使显式普通 start 可以核验并
接管已经运行的原核心，持久记录本次请求，而不重复启动或改写当前配置。
停止状态仍由 T032 的 Start 重新核对完整停止证明后启动；不明状态保持拒绝。
契约见[managed-start v1](../../specs/001-hoopa-proxy-manager/contracts/managed-start-v1.md)，
机器索引见验证结果（本地原件 `../../experiments/gost-mihomo/p1-managed-start-validation.json`，不随Git上传）。

这项有限控制模块已验证；产品 CLI、管理服务发现/就绪和 service.stop 尚未接入，
T033 仍未完成。它不能由服务初始化、control-only 或查询自动调用。

## 请求与恢复

相同 ID/内容先返回原持久 Result，再考虑当前 revision/context；不同内容复用 ID
拒绝。running 分支在接纳前核对原发布操作、唯一 RunID 和完整原实例证明，接纳后
及提交前重复核验。独立 adoption 记录不成为核心的创建操作，也不改变 active、
revision、generation、出生/注册、目录或配置，不调用材料 Source。

显式 Recover 识别并核验 adoption 原件，再完成原目标和独立恢复调用；不 spawn、
重登记或 activate。prepared 只有 `{}`、证据损坏或原实例不可证时明确 degraded。
无效 adoption evidence 的非终结原目标与恢复调用分别标为 degraded，原 evidence
逐字保留；已终结目标不改写历史，仅本次恢复失败。

## 一个实际核心场景

Linux/amd64、UID 1002，固定 basic 核心 SHA256
`2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。

1. 公开 Apply 首启，核心 PID **2569153**、start tick **41257745**，RunID
   `run:e137bf973ba2e82ce504400503034f0a`。
2. 公开 EnsureStarted 接管。完整进程身份与原 RunID 一致；仅新增 adoption journal。
3. 关闭并重开 Coordinator/writer，公开 Recover 对已经提交的 adoption 重新取得
   原实例证明；核心身份不变，没有新增运行。
4. 原 start 与恢复请求精确重放；各阶段共四次授权本地 HTTP echo，Source 总共一次。

除操作 journal 外，受保护文件树的字节及检查到的 mode/mtime 保持一致。三次快照
归档 36 份私有原件，包含 bundle/config/log 及运行/操作记录；认证材料不公开。
最终通过原 pidfd 退出观察与 `/proc` 消失核对清理；没有另行采集私有 Child.Wait
退出码。writer Close/reopen 是本次边界，不冒称管理器 OS 进程被杀；无 VM/BPF。

## 版本与检查

| 检查 | 结果 |
| --- | --- |
| V1 初轮合成 | 6 顶层/25 子测试，31 pass、0 fail/skip；真实 Store 与合成 verifier，涵盖顺序、五阶段中断/重开恢复、身份错配、重放与拒绝零写 |
| V1 受影响补强 | 2 顶层/24 子测试，26 pass、0 fail/skip；增加 publishing/committed 前证明变化及未证/degraded 的早期拒绝；不与初轮相加 |
| V1 实际核心 | 2 顶层 pass＝1 实际场景＋1 空 dispatch，0 fail/skip |
| V2 无效证据处理 | 1 顶层/4 子测试，5 pass、0 fail/skip；Version/base 错误在 pending/terminal 两类目标上的处理，零 verifier 调用 |
| 最终 V2 control 回归 | 436 pass、0 fail/skip，61 顶层/375 子测试，56.22 s，输入摘要未变；包含最终 7 顶层/33 子测试的 managed-start 合成用例 |
| 静态及格式 | 最终 control vet 退出 0，四个新增/受影响 Go 文件 gofmt 与空白检查通过 |

最后一次源码修订仅改变 adoption 解码失败路径：原目标曾可能继续 pending，而
恢复调用进入 degraded；现两者均明确显示未解决状态，并保留原件。该问题来自根
审查，修订后有独立定向检查；不能声称在修订前跑过这四个反例。

实际样本运行于 V1 的正常接管路径；V2 保留该路径，只增加解码失败处理。实际样本
没有重跑，不改标为 V2 实测。V1 两个生产文件的归档副本是事后按已记录 SHA256
精确重建，摘要匹配执行回执；它们不是执行前字节归档。最终 V2 回归另存执行前源码
副本。T032 的 480-pass 回归与此处 436-pass 分属不同版本和包范围，不合并。

root 负责接管/恢复语义、实现与最终核验；executor 实际 gpt-6.1-sol / medium 完成
限定测试与原件封存，scout 实际 gpt-6-luna / high 独立核对稳定源码及证据，没有再
派生。服务停止/旧请求重放、端点发现、启动就绪、产品命令和 T034 完整矩阵继续待验。
