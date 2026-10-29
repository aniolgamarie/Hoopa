# Source profile 5：受限实例与程序字段能力

状态：2026-10-25，接口已实现；独立0015候选已干净构建并通过[实际preflight和v5正例](../../../docs/validation/connector-field-profile.md)，[T132受限验收](../../../docs/validation/t132-restricted-acceptance.md)及T137冻结完成。namespace三模式仍保留其0014/v4原hash结论；旧帧和正式G01不改判。生产部署、PROCESS规则和正式100流按后续任务验收。

## 能力含义与信任前提

固定能力ID为 `linux_amd64_loopback_connect_v1`，归属定义为 `connect_initiator_v1`。能力只适用于已登记的6.18.54.1/amd64内核/BTF、scoped executable BPF对象、受控初始namespace与root/leaf、普通非TFO IPv4 loopback TCP、先挂接后创建目标socket，以及可信loader/管理员。原FD持有者、写入者和转交者不能覆盖连接发起者历史。实例使用boot ID、初始PID namespace、原TGID及leader start BOOT ns；caller TID另存，不能以单个PID或core/capture世代替代OS实例。

`available` 表示该候选在上述范围内承诺且逐连接校验的字段；不是协议parser对外部数据的真实性认证，也不是任意Linux环境的支持声明。加载器负责核对真实固定资产；核心负责实际对象、配置、范围及原accepted→connector准入。具有可信管理权限的一方运行中替换内核/hook/共享fs root不在该profile威胁模型内。任何必要证据缺失、冲突或capture fault仍拒绝新连接，不发布未知身份的成功opened，不猜配。已有合法历史不被后来的世代失效改写。

字段分为 `instance`、`program_name`、`program_path`、`comm`。前三项可用；comm仍未验证。程序名来自同次connect的exe路径basename，不代表脚本名、kernel comm或产品显示名。路径是原字节快照，不是永久文件身份；保留删除后缀及UTF-8有效性。非UTF-8字段可保留原字节，不能把空的展示字符串用于名称匹配。不同来源/版本不能按同一字符串混合分组。

## 单一纯协议定义与移交

新增 `protocol/identityprofile`，只含此有限profile的常量、值类型、严格解析/需求校验；不做I/O、加载、修复、推测或动态能力探测。coreadmission、wire和protected config共用定义，集成到上游时保持同源，只调整import/package路径。使用固定字段集合，不建立插件或通用能力注册框架。

新增 `hoopa-core-identity-handoff-v4`：保留v3的全部字段和21FD，额外且仅额外增加 `identity_profile: "linux_amd64_loopback_connect_v1"`。旧v1–v3继续拒绝额外字段。新v4仍采用相同scoped对象和24B map，不能复用map reserved或改变112/296B ABI。只有该新handoff映射到source event 5；旧scoped v3继续映射4。

bootstrap在原 `scoped-executable-online` 上增加显式 `--identity-profile` 选项；未指定时行为不变，指定时仅允许此ID和此BPF profile。新模式必须核对真实BPF SHA256 `df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49`、libbpf SHA256 `1c928f61158f25d39cd3fbc7ddafeebec2448886225e2c1824455fcfcd15d04c`、内核release及BTF SHA256 `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9`；继续从已打开且摘要核对的原core FD exec。核心二进制摘要由新的不可变候选manifest/运行器固定，不能把自报版本当摘要。实验核心仍要求专用guest；不借本契约移除现有环境守卫。

Owner先完成现有Adopt对象/namespace/frozen-map检查，才提供新能力描述。每次Admit的锁、link一次入口核验、config/gate/fault顺序和拒绝语义不变。24B范围原文来自已校验的实际map lookup：可将成功checkIdentityScope读回值保存为Owner内的值字符串，Adopt为单线程、Admit已有mutex；失败不能更新它或产生grant。新Identity携带值副本，不暴露可变map/slice或允许以后修改历史。旧Identity序列化与旧source writer不增加字段。

## 严格source事件

`schema_version=5`，单epoch禁止混版本。非opened事件沿用v4生命周期、质量、路线、整数计数及终末格式，版本仍为5。所有profile开关必须同时支持5，包括quality初始化和实际线路采集，不能仅修改opened writer。

opened的顶层字段集合与v4相同。legacy `process` 精确为 `{ "status": "unknown", "reason": "legacy_process_fields_unused" }`；它只表示旧process字段没有被此协议使用。新的字段能力及来源以connector为准，不从Mihomo当前`metadata.Process/ProcessPath`回填，也不据此调用旧PROCESS匹配器。

connector schema为 `sourceidentity-core-admission-v3`。它保留v4 connector除 `identity_capability`、`process_path` 两个legacy字段以外的全部字段，增加且必须具有 `field_profile`；这两个被移除字段即使值为unknown也拒绝。原56/112/40B坐标核验不变。executable字段集合与v4相同，但其schema为 `sourceidentity-executable-call-v2`、status为 `available`，source/name_source及296B的精确字节、cookie/TGID/TID/token/时间/路径验证不变。comm原文及unverified/unavailable状态不升级。

`field_profile` 精确具有以下字段，未知/重复/缺失/null一律拒绝：

| 字段 | 固定值或约束 |
| --- | --- |
| schema | `sourceidentity-field-profile-v1` |
| id | `linux_amd64_loopback_connect_v1` |
| definition | `connect_initiator_v1` |
| handoff_schema | `hoopa-core-identity-handoff-v4` |
| source_schema_version | 整数5 |
| scope_raw_hex | 实际读取的24B小写hex；generation等于connector generation，user/mount inode非零u32，schema_version=1、reserved=0 |
| fields | 精确包含下表四个对象，每个对象精确具有status/source |

| fields键 | status | source |
| --- | --- | --- |
| instance | `available` | `linux_bpf_connect_task_v1` |
| program_name | `available` | `executable_path_basename_v1` |
| program_path | `available` | `task_exe_file_d_path_v1` |
| comm | `unverified` | `linux_bpf_task_comm_v1` |

strict decoder可在先核验v5精确形状后，将临时副本归一化以复用v4原坐标验证，再恢复新typed字段；不能修改Raw、放宽旧decoder或让旧legacy capability/path呈现新值。新typed结果显式暴露FieldProfile，Connector的已删除legacy字段保留空值；消费者必须显式识别v5，不能用旧字段空值解释为直连、零流量或已验证。parser成功只证明格式、绑定和守恒，不替代候选信任及运行验收。

## 配置期需求与listener前拒绝

保护配置新增且仅用于新profile的 `hoopa-identity` 对象：精确具有 `profile_id`、`source_schema_version`、`required_fields`。前两项分别为上述ID和整数5；required_fields为1–4个不重复的已定义字段名。当前可接受instance/program_name/program_path的非空子集；要求comm必须拒绝。此对象是运行前数据能力需求，不是进程路由规则，不接收路径值或隐式切换规则来源。

保持现有 `Validate` 的旧形状与PROCESS/UID拒绝。新增显式验证入口读取新对象，复用原完整配置验证，并与Owner实际采用的profile相等核对；缺能力、旧handoff、错source版本、未知字段/来源、required comm均在开放listener前失败。旧profile夹带新配置拒绝，新profile缺少新配置也拒绝；仅设置环境变量不能跨过Adopt。配置摘要仍针对完整原始配置，实际解析和需求校验必须发生在同一启动路径。即使name/path可用，所有旧PROCESS规则继续拒绝，直到其独立消费路径被后续任务实现。

## Manifest与重开

新source使用manifest `schema_version=2`，在旧manifest所有字段上仅增加 `source_schema_version:5` 和 `identity_profile:"linux_amd64_loopback_connect_v1"`，scope改为 `linux_amd64_tcp_scoped_connect_initiator_ipv4_loopback`。原source epoch计算、nonce、二进制/启动实例和资源上限不改；旧manifest v1只接受原精确字段/scope，不得附加新键。

新增显式manifest/event一致性校验入口，核对epoch、config_generation；新manifest仅接受source5，opened的profile ID必须一致，旧manifest不得承载source5。接收/检查工具及核心旧来源扫描在已有验证位置调用它；不改变锁、帧写入/同步、ACK、段回收或gap推导次序。新旧epoch可分别恢复，原始帧逐字保留，同一epoch禁止切换profile。新v5的完整及崩溃未终末fixture要验证重开结果与重复恢复稳定；恢复报告不补写历史身份，也不把pending变成零。

## 最小验收和任务边界

先做纯协议/配置/Owner的定向race测试：全部新字段和raw绑定反例、旧版本严格性、非法UTF-8与大整数、manifest错配、profile/handoff/配置缺失与错配、明确comm不可用、所有PROCESS仍拒绝、来源完整与pending重开稳定。新增任务要求的 `experiments/mihomo-events/identity_preflight_test.go`，必须覆盖实际受保护启动路径在listener前拒绝；未提供实际候选时集成部分明确skip，不算通过，纯需求校验仍可执行。

然后在干净副本生成独立0015补丁/新manifest/二进制，完成实际同拓扑正例和启动前拒绝负例；正例字段须与原socket oracle、24B/296B、原PID/start、两线路ready与33/33终末一致。不能把0014真实结果直接赋给0015。既有namespace/FD/lifetime证据按真实diff解释保护范围，不因新增序列化而重做全部矩阵；新证据发现采集/准入语义改变时再扩大。

本接口承担T132字段能力与preflight；T132最终裁决和T137固定候选已在独立记录完成，见本页状态链接。T127正式100流、T136控制harness和P2规则继续分别验收。旧G01 0/100、旧v3/v4 unverified及失败记录完整保留。

补充接口边界：旧`ReadSourceLifecycle(reader, expectedEpoch)`只作无身份的生命周期/计数投影，可不带manifest读取v5；不能据此授予身份能力。核心来源扫描/写入、manifest-aware receiver及ReadSourceRecovery使用显式manifest绑定。不能概括为每一个只读helper都验证manifest。
