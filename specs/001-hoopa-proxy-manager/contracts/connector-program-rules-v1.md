# Source5 原连接程序规则：有限实验契约

2026-10-25。T127正式登记前的能力补全契约。配置/核心实现、0017干净构建及14类实际规则校准已完成；见[有限验收记录](../../../docs/validation/connector-program-rules.md)，不计入正式100槽位。按已确认的 `connect_initiator_v1`，规则使用发起原TCP连接时捕获的程序名称/路径；FD共享、转交、后续exec或comm改名不改写该连接的决策。原[字段契约](source-connector-v5.md)、旧0015/0016候选及旧0/100结果不变。

## 显式入口与兼容

新增独立配置入口 `ValidateDeclaredProgramIdentity` / `ValidateWithProgramIdentity`，与原 `ValidateDeclaredIdentity` / `ValidateWithIdentity` 分开。新入口接受相同完整受保护配置及 `hoopa-identity`，可选增加精确对象：

```json
"hoopa-program-rules": {"profile_id": "connect_initiator_exact_program_v1"}
```

有此对象才允许下述两种程序条件；未知/重复键、null、错误类型/版本均拒绝。无此对象使用原校验，所有PROCESS/UID继续拒绝。旧校验入口即使收到新对象也必须拒绝，避免现有0016控制helper把新能力误判为可应用。新核心protected启动显式调用新入口；旧profile/旧handoff、未启用trace或非source5均不能开启程序规则。

配置中每个程序条件的字段必须出现在 `hoopa-identity.required_fields`；要求 `program_name` 或 `program_path` 后，启动时还须核验实际Owner采用的受限profile。单独声明字段available不会开启规则。实际运行仅限可信管理员管理的固定Linux/amd64、普通IPv4 loopback TCP与已登记cgroup/namespace；其余前提沿用T132受限裁决。

## 有限语法与三值

仅扩展受保护规则中的 `PROCESS-NAME,value,target`、`PROCESS-PATH,value,target`，以及已有单层AND中的这两种原子条件。target仍只能为已声明node/group或REJECT，规则与AND成员顺序不重排。既有DOMAIN、DOMAIN-SUFFIX、IP-CIDR、IN-NAME与末尾MATCH规则保持原语义。

- NAME比较原exe路径的basename，PATH比较原exe路径文本；严格区分大小写，不归一化、不解析symlink、不strip `(deleted)`，不使用comm、argv、解释器脚本名或产品名。
- 模式为1–255字节合法UTF-8，禁止控制字符、空白、逗号和括号；NAME不含 `/`，PATH必须以 `/` 开始。此有限语法不承诺支持所有合法Linux路径；不能悄悄截断或改写模式。
- 不支持PID/instance/UID条件、WILDCARD/REGEX规则类型、OR/NOT、嵌套AND或隐式DIRECT。模式中未被上述字符集禁止的 `*`、`?`、`[`、`]` 仅为普通字符，逐字比较，不作通配或正则解释。数据中的instance可用与instance规则能力分开。
- 当前flow必须具有经过校验并按值冻结的source5 connector、available executable及精确field_profile。对应文本valid=true时，相等为true、不等为false；缺失、版本/绑定不符或该文本valid=false为unknown。PATH文本不可用不自动使合法NAME文本不可用，两字段分别判断。
- 不把connector填入旧`process`字段；其`unknown/legacy_process_fields_unused`保持。独立的新消费函数读取原connector证据；source3/4和旧matcher的行为不变。
- 首条true决定目标。遇到可能改变结果的unknown时拒绝该连接，不继续落到末尾MATCH。AND中任何已知false支配unknown，全部true才为true，其余为unknown；要验证unknown在前/后均遵守此真值表。

## 运行绑定和只读声明

程序规则profile在source初始化的同一 `once` 完成前绑定到该source，不能运行后开启或重写；重复Prepare若请求不一致则拒绝。protected完整配置验证、实际Owner能力核对、source准备必须都先于Bind/Listen。启用不改变source5/manifest2的字段含义、日志/锁/ACK/GC顺序或旧流路由。

保留原 `GET /hoopa/status` schema1和三个legacy identity=false字段，不用它们表示新消费器能力。显式启用新profile时增加只读 `GET /hoopa/program-rules`，沿用相同controller认证，精确返回schema_version=1、profile_id、source_schema_version=5、name=true、path=true、instance=false、comm=false。未启用时返回409；无凭据仍先401。此声明只表示本进程已准备的规则profile，不代表每条连接的文本都已知。现有0016控制helper继续拒绝新配置；新能力校准由独立guest直接启动固定新核心，不虚构现有helper已集成。

## 实现与验收顺序

1. 先实现纯配置入口与按值的程序条件匹配，定向race验证合法/非法模式、重复/未知字段、required_fields覆盖、旧入口拒绝、known-match/nonmatch/unknown及AND真值。公开纯校验与核心副本保持明确逐字/导入路径对应；不得放宽旧Validate。
2. 接入核心：仅新配置入口开启消费器，在同一次source准备中冻结profile。既有source5原始字段和事件版本不变；实际目标、ready与计数继续由同一flow/attempt产生。
3. 从0016干净副本形成追加补丁和独立制品；新候选必须有真实原程序NAME/PATH命中、不命中后走下一条、大小写不等、AND顺序和unknown拒绝。通过复制执行文件到非UTF-8 basename/path在guest制造文本不可用；必须先证明实际admission成功并写出对应raw/valid=false的opened，才能算rule-time unknown，不能用admission提前拒绝替代。
4. 拒绝样本核对没有目标dial/应用写；known-false与AND false-dominates-unknown样本须核对实际后续节点、应用字节和完整来源。非UTF-8目录但合法basename用于分别验证PATH unknown与NAME known。需要新实际环境前提失败时保留诊断，不算能力通过。
5. 冻结新hash的CapabilityRecord与真实diff影响范围后，T127才登记新source5 oracle和100个正式短流槽位。先行校准、预期拒绝边界与正式100个正例分母分别登记；未知/缺失/超时不得从已登记正例分母中删掉。旧host/legacy oracle与0/100完整保留。

本契约只补P0实验消费器，不提前交付P2应用规则CRUD、规则编辑器、生产launcher、完整账本或TUI。实现和小样本通过也不等于T127正式矩阵或P0门槛通过。
