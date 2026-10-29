# T130：实际拓扑的前提诊断

日期：2026-10-23。T130完成的是诊断取证，身份能力仍未通过。机器结果见摘要（本地原件 `p0-prerequisites.summary.json`，不随Git上传），既有正式100流[失败](native-candidate.md)原样保留。

## 实现与真实观察

新增有界只读`InspectVisibility`及显式实验输出器，按`/proc/PID/status`的实际EUID分类，复核PID/start/UID，逐项检查FD目录和readlink；权限拒绝、退出/竞态、截断及取消都不能成为passed。最多4096进程目录项、每进程4096 FD、合计65536 FD和16条blocker；不输出argv、环境或FD目标。passed仅代表本次扫描可读，跨UID共享、FD转交及非原子集合仍unverified。

| 观察条件 | 结果 | 解释 |
|---|---|---|
| 默认工具沙箱内扫描 | 4个同UID进程全部可读，19个FD链接；scan passed | 同时回环bind返回EPERM，不能在此条件运行代理实验 |
| 可运行回环的实际执行拓扑 | 803个可见进程、64个同UID，63个FD集合完整可读，1项FD权限拒绝；scan failed/owner_visibility | 核心、两个同名源进程、observer为同一driver的子进程；UID/netns/PIDns匹配，两个原socket均held后才采样 |
| 固定候选本身的两条前提样本 | 2个opened/closed，终末字节/域名2/2；源身份0/2、owner_visibility 2/2 | 不是正式100流验收，原100流分母和失败保持不变 |
| 私有持久目录 | 0700目录、0600文件，file/dir Sync及第二writer拒绝通过 | 只证明该次最小文件行为，不替代满盘/断电/SQL矩阵 |

这明确区分“沙箱内扫描可读”和“实际可运行核心的环境可读”。不能将前者的通过结果移用于后者；不是仅凭普通命令的`/proc`快照推断核心权限。observer是不同可执行文件，其完整安全上下文等价性仍unverified；实际候选的自身ProcessEvidence失败与该扫描相互支持，不把observer结果当作核心的完整权限证明。

核心仍为`76e0e114ac9a669ec0eb131f35ead972d3e24b2dceeb270e72a3e879f5b9c32e`，没有修改/替换。独立采集的核心PID/start与source manifest相符。两条源请求在父进程持久登记真值后、两个socket同时持有的barrier上完成诊断，随后才允许发送CONNECT。新增observer模式固定为两条诊断样本，禁止与`--require-identity`或100流模式混用。

## 验证和其他资源

固定Go1.27.3：诊断库6项顶层/7项子测试race通过、0skip，vet/格式通过；实验输出器构建通过。Python诊断参数/hash/拓扑首轮4项通过；审查后补齐已验私有执行副本、malformed observer及barrier异常EOF释放，现7项通过。原真实诊断结果保留，新代码未被用来重跑身份样本。未运行正式100流或全量故障矩阵。原始诊断与truth/frames位于私有`/tmp/hoopa-native-probe-6sgo4bn6`；沙箱/文件系统材料在`/tmp/hoopa-t130-run-xvhhtx2j`，环境盘点在`/tmp/hoopa-t130-env-qrni0ohl`，均可能被临时清理。

固定Go/核心/16份候选源码摘要仍匹配；CGO启用且gcc可用。不据此宣称SQLite驱动已验证。未发现现有适用JSON Schema验证器；真实SSH/注销/休眠环境与免费CI授权未建立证据，均unverified。没有安装依赖、修改权限、开启系统代理或默认特权。

## 裁决

T130按“完成诊断可保留失败”关闭；完整身份前提不成立，T132正向实施/验收不启动，T137及正式T127仍阻断。具体原socket证据算法、未解决的证明范围和允许重试条件见[ADR006](../adr/006-source-identity-proof.md)。不依赖正向身份结果的T133–T135独立P0子任务仍可按各自前置推进。
