# Source profile 4：同一次 connect 的 executable 快照

状态：2026-10-24 接入前协议登记，尚无 profile4 核心运行结果。依据[调用编号原型](connector-executable-call-token-v1.md)的有限验证，不提升正式身份、进程名/路径规则或平台能力。v1–v3 原协议、原始帧和恢复结果保持各自原义；v3 仍只接受 `process_path=unknown` 与56/112/40字节的原三份证据。

## 事件与兼容

`schema_version=4`。同一个 source epoch 不能混用版本；opened 后的 ready、route、checkpoint、closed、gap、epoch_closed 沿用 v3 的字段与 v2 的生命周期/连接段守恒语义。检查点上的版本保持4，不转换成3。仍只有 opened 携带 connector，其他事件不能补写或覆盖身份。

opened 顶层字段、identity_scope 和 `process={status:unknown,reason:connector_experiment_unverified}` 与 v3 相同。connector 保留 v3 所有字段和严格原始绑定检查，但 schema 改为 `sourceidentity-core-admission-v2`，增加且必须具有 `executable` 对象。legacy `process_path` 仍是 `unknown`；不能借新增字段意外启用现有 PROCESS-PATH/PROCESS-NAME 规则。原 `identity_capability`、leader/caller comm 状态仍未认证。

## executable 的完整字段

所有列出的字段必需，其他字段拒绝，重复JSON key、非法JSON Unicode、非有限数值等沿用 wire 的严格解析。

| 字段 | 约束 |
| --- | --- |
| schema | `sourceidentity-executable-call-v1` |
| status | `unverified`，仅表示保存有限实验原始证据 |
| source | `task_exe_file_d_path_v1` |
| name_source | `executable_path_basename_v1` |
| call_token | 非零、无前导零的 u64 十进制字符串 |
| observed_boot_ns | 同口径非零 u64 字符串，位于 connector leader_start_boottime_ns 与 observed_boot_ns 之间（含端点） |
| path_raw_hex | 内核返回的路径字节，不含末尾NUL，1–255字节、绝对路径、小写hex，不截断或去掉 ` (deleted)` |
| path_text / path_text_valid | 原字节是有效UTF-8时为精确字符串/true；否则为 `""`/false，保留raw bytes |
| program_name_raw_hex | path最后一个 `/` 之后的原字节，必须非空，小写hex；不剥离删除后缀、不猜解释器脚本名 |
| program_name / program_name_text_valid | basename原字节独立判断UTF-8，与path_text采用相同的精确字符串或空字符串规则 |
| raw_record | 完整296字节小写hex；包含路径缓冲区末尾未解释的原字节，不能重建后丢弃 |

ABI little-endian：cookie@0/u64，TGID@8/u32，TID@12/u32，path_result@16/i32，consumed@20/u32，BOOT ns@24/u64，call_token@32/u64，path[256]@40。cookie/TGID/TID必须分别等于原 connector 的 client_cookie/tgid_initial/caller_tid_initial；consumed 必须为1；path_result必须为2–256，路径首字节 `/`、返回区间最后一字节NUL且此前无NUL。path_raw_hex精确等于该返回区间除NUL；时间和token与展示字段一致。raw_record其余字节原样保留、不解释为路径。

该对象证明边界仍是候选的建连过程快照。实例键继续是原始connector PID/leader启动身份及坐标/世代；程序名仅是明确来源的basename，不能与comm或应用显示名混合归并。历史不根据当前 `/proc` 回填，FD转交/exec/改名不改旧帧。

## 纯协议验收与核心接入分工

先执行纯wire的合法v4/非法字段与原ABI逐项反例、非法UTF-8可逆表示、超过2^53的整数、旧v3严格保留、混版本epoch拒绝、完整生命周期重放与稳定恢复。纯parser通过不代表 producer、关联或内核验证通过。

核心接入另需新BPF对象/配置、9map/6program/5link的明确移交（20FD，不能放宽旧11FD白名单）、原accepted见证、LSM记录与旧connect记录同cookie/TGID/TID/时间核对、只有consumed1可进入opened。组合对象的LSM/raw调用状态不自动继承旧writer gate的全量停止计数；只能按已消费的不可变记录解释，不能把旧gate排空当作所有新hook停止证明。准确加载、挂接、移交和实际两协议正反回归完成后，才可评估新profile最小能力；T132/T137及正式G01门槛仍保持。
