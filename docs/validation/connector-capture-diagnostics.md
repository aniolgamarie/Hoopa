# 只读采集诊断的实际验证

2026-10-24，[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-capture-diagnostics-v1.md)在独立 001–013 核心与隔离 VM 上通过。新增 GET `/hoopa/capture-health` 已能展示实际采集故障；现有 status 的计数继续只表示 accepted admission。完整输入与观测见机器记录（本地原件 `../../experiments/sourceidentity/capture-diagnostics-validation.json`，不随Git上传）。

## 解决的问题与行为

此前范围负例在 BPF 阶段拒绝新 connect，但核心 status 仍是 running、rejected=0、revoked=false。新接口经原有 loopback/Bearer 认证，分别读取 capture fault、exe fault bits、两个 raw syscall 程序的递归遗漏计数，返回独立版本的诊断对象。

计数使用十进制字符串保留完整 uint64。读取失败是 unavailable/null，不能伪装成零；旧 profile 不适用字段是 not_applicable/null。关闭 Owner 后不再访问其 FD。诊断不修改 map、不关闭资源、不撤销 generation，也不自动修复。接管和新连接准入另行要求两个 raw recursion 计数可读且为零，失败按既有保守拒绝路径处理。

## 实际观测

固定核心 SHA256 为 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`，新镜像为 `39b98c9290911b11805c48112c45c7b9a6d42caecc058041c83885f95198c475`。原组合对象与固定 6.18.54.1 内核继续单独核对；20FD 接管、CLOEXEC、BTF 和身份原始记录符合。

| 阶段 | capture_faults | 诊断 state | 原 admission status |
| --- | --- | --- | --- |
| ready | `"0"` | no_fault_observed | 0/0/false |
| 正常 HTTP 基线结束 | `"0"` | no_fault_observed | 1/0/false |
| 范围外 connect 被拒 | `"1"` | fault_observed | 1/0/false |
| 正常新 socket followup 被拒 | `"1"` | fault_observed | 1/0/false |

每阶段读取两次诊断，八次读取均成功，各阶段两次相等；前后旧 status 逐项相同，运行状态仍为 running、连接 gauge=0。其余三计数均 observed/`"0"`。这些值由实际核心执行 BPF 查询，经诊断接口返回；本轮没有额外独立转储 map，也没有注入真实 raw recursion skip。

基线原 PID/TID 为 83，exe `/bin/python3`、dev2/inode8、call token1，296B 原始记录匹配原 socket 和同调用时间区间。HTTP 到 alpha.test/node:a 的 ready 为 11ms；发送33B、提前 SHUT_WR 后完整收到33B和EOF。范围外 PID86 与正常 followup PID87 的 connect 均为 EPERM，零协议/应用写入。最终仅有基线一条流、完整7帧 epoch，closed/final=33/33、tail=0、无gap。核心正常 SIGTERM 退出0，23份原始导出摘要全部重验。

## 检查与边界

公开 Owner 整包 race 为34顶层/233含子项通过，其中新增诊断为6/89；对应 vet 通过。重新应用全部补丁后，在集成源码执行新诊断及端点检查，7顶层/100含子项和 vet 通过。独立镜像准备56项通过；首次 host `--help` 因缺少 helper 导入路径失败，记录保留，仅给离线命令增加明确 PYTHONPATH 后修正。实际 VM、canonical extractor、checker 均退出0。

计数读取是 sequential_reads，内核写入者不会被 Owner 锁暂停。`no_fault_observed` 只表示本次适用计数成功读到零；它不证明所有 hook 持续执行。固定内核的普通 LSM MAC 钩子没有同样的 recursion miss 计数保证，不能从两个 raw 程序的零读数推断其完整性。单元测试中的不可读、并发 close、非零 recursion 注入与本次实际 VM 证据分别记录。

本轮只有新核心的一个 outside-source 场景，其他候选的正例与矩阵不自动继承。创建时未运行的[候选 manifest](../../experiments/mihomo-events/core-capture-diagnostics-candidate-manifest.json)保持原记录，本页追加实际结果。继续验证新组合活动流崩溃/重开、hook 连续性与正式完整矩阵；T132/T137 未勾选，正式 identity/name/path 仍 false/unverified，G01 新增0，原0/100和P0/P1门槛不变。
