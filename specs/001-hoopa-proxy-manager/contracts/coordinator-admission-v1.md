# 协调器首次接纳 v1：T029

2026-10-27，root 决定。本文规定 T029 的实现与验收边界，尚不表示实现通过。
T030 执行核心效果，T031 实现显式恢复；本层只允许返回 prepared/pending 收据。

## 所有权与串行范围

协调器通过显式 OpenCoordinator 打开并独占一个 writer Store，将 Store 和 Journal
保存在私有字段，不接收调用方同时持有的 writer，也不导出这两个可写句柄。服务启动
时的打开/初始化与请求接纳分开；旧 revision 请求的“零写”从已打开服务收到请求计。
同实例第二个 writer 由已有 flock 拒绝。

同一个协调器的写请求和 Close 使用同一 mutex。所有句柄的值副本共享内部状态和
同一 mutex，不能复制锁后继续共用一个 Store。锁涵盖重放、当前状态读取、输入复核、
首次接纳，并由 T030 的完整 Apply 继续持有至本次执行结束；不能把接纳后解锁当作
允许外部调用者自行执行效果的许可。Close 等待在途写流程结束再释放 Store/flock。
不扩展 Store 的短方法 mutex，不在控制锁内获取相反顺序的锁。

T029 的 AdmitApply 仅用于接纳阶段的内部组合，不启动进程、发布 bundle 或 active、
运行核心校验、恢复、下载或清理。prepared 也会阻止其他新 apply；同请求重试只返回
旧记录。T030 调用持锁的内部接纳函数并继续执行，而不是在公开接纳方法返回后另取锁。
本层不是跨日志、active 与进程的原子事务。

## 接纳顺序

1. 严格解析完整请求身份；持控制锁后先 Journal.Lookup。已接纳的同 ID/同内容返回
   当前已存记录，不调用当前 Intent 验证器/输入读取器，不因旧 revision、容量或后续
   能力变化重做副作用。异内容 ID 返回 REQUEST_ID_REUSED。
2. 新请求须为 v1 config.apply，params 精确包含 plan、confirmed_plan_digest、
   accept_disruption，字段类型、存在性和大小都校验；Plan 通过 T028 严格解析及
   必需 Intent 验证器。确认摘要必须等于 Plan 摘要，blockers 必须为空；需要中断时
   必须有 true 许可。P1 仅接受 start/replace，select/none 返回能力未支持。
   当前 basic 完整替换没有已验的跨连接保留能力；replace/preserve_old 也返回能力
   未支持，不因调用方重算摘要就允许绕过中断确认。
3. ReadActive 的 revision 必须等于 base_revision，previous_generation 必须与当前
   active 一致；新 target_generation 不能等于 previous_generation。差异返回 CONFLICT。
   顺序不依靠单条 Store API 的 mutex 推导多条 API 的一致性。
4. 在同一控制锁内枚举并完整读取有界操作日志。任一非终态（包括 prepared/degraded）
   阻止新 apply，返回 RECOVERY_REQUIRED；损坏或不可读记录明确失败，不能当作空历史。
5. 必需的内部 CurrentInputs 读取器只读本地固定来源，返回实际 compiler version、
   capabilities digest、全部输入摘要及**独立计算的完整 impact**。按 kind/id 比较完整集合，拒绝遗漏、多出、
   重复或摘要变化，顺序变化本身不构成来源变化。不能以计划中声称的摘要直接充当
   当前观测。缺失读取器不得接纳；读取失败不能回显路径/秘密。T037–T041 提供完整来源
   语义，固定验收适配器须绑定实际预验证 fixture，不把测试函数冒充生产编译器。
   CurrentPlanInputs.Impact 从已验证的当前/候选配置和运行事实计算；逐字段核对
   requires_disruption、affected_apps（含顺序）、新旧 generation、connection_effect、
   runtime_action，任何变化返回 CONFLICT。不能为空，也不能直接回传 Plan.Impact
   作为证明。摘要只证明确认的内容一致，不能证明“不受影响/不中断”等声明真实。
6. 所有检查和取消检查通过后，生成新的 target_revision，并 Journal.Admit。首个
   写入只保存 prepared/pending 收据；失败、不确定或 created=false 不授权效果。
   Intent.candidate_digest 绑定完整 Plan 摘要，impact_digest 绑定规范化 impact；后续
   bundle 的 manifest digest 另存步骤证据，不拿二者互换。

任何校验失败都不创建本次 operation，不发布候选、修改 active 或运行状态。不会自动
更新订阅、重新编译或合并候选。输入读取器和 Intent 验证器都是内部受信代码，不能由
JSON 请求指定实现；依赖可返回不可用，但重放已知记录不重新调用这些依赖。

## 验收

使用实际私有 Store/journal 检查每类拒绝前后文件的字节、大小、权限及修改时间，覆盖
旧 revision、影响/确认不匹配、每种输入变化/缺失/重复、编译器/能力变化、未知操作/
参数、取消、pending/degraded/损坏、历史容量，以及旧请求先重放。另覆盖影响独立
复算差异、伪造 preserve_old 并重算摘要仍被拒绝。
并发请求须证明只有一个首次接纳，其他请求不穿插；Close 在在途输入核验完成前不能
释放 writer，同一实例另开 writer 失败，显式关闭后才可重新打开。
所有通过均为接纳模块证据；C01 的完整提交后重放与产品死亡恢复由 T034 另验。
