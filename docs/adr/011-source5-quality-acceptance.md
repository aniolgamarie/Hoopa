# ADR011：固定 source5 候选的质量与线路验收范围

2026-10-26。状态：实施决定已由[正式6VM/21槽](../validation/source5-formal-quality.md)验证，T129/T014完成；P0/P1未放行。承接[ADR008](008-remaining-evidence-execution.md)、[受保护入口契约](../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)及[准备证据](../validation/source5-quality-preparation.md)。本决定区分可达网络路径与受控语义检查，修正正式验收的执行方法；不改写历史v2契约或扩大产品能力。

## 决策依据

固定0017二进制SHA256为`5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`。该候选在受保护入口只允许普通DIRECT、HTTP CONNECT、SOCKS5及受限组/嵌套路径。旧T134的在线selector写入接口在此入口返回拒绝；既定更新方式是独立新进程、入口、端口和generation，旧连接排空。客户端断开也不会取消从Background派生的上游dial context；精确配置白名单不允许TFO。继续为这几种不可达行为编造网络样本，无法提供有效证明。

另一方面，来源仍须正确表达其实际收到的cancelled、unknown及部分写入结果。FR-046/047要求准确区分测量和结果，并不规定“关闭客户端必定产生cancelled”。FR-048/SC-013要求原连接历史不随当前选择改变，不要求通过未经允许的原生controller完成更新。生产接入端口、应用重启和长连接策略仍由后续接入/更新任务交付；这里不把不同端口实验称为稳定端口热切换。

## T129分层验收方法

1. **实际固定核心矩阵**：全部来源使用同一0017、source5和既有connect_initiator_v1原进程oracle。六场独立VM，共21个原应用连接槽：HTTP回复边界2、SOCKS完整BND边界2、DIRECT慢DNS2、拒绝/畸形/超时7、完整替换4、嵌套及失败后成功重试4。六类准备通过后，在首个正式VM之前冻结六份新formal镜像、全部实际slot/token/seed、输入与容差，并以总matrix和一次性claim执行。新正式21槽全部通过；此前准备仍各自formal0，没有重命名或重评为正式结果。
2. **来源事件与计量语义**：消费同固定源码的source5受控输入检查，验证failure/timeout/cancelled/unknown、ready证明与finish分离、已知结果不被unknown或后续Finish覆盖、同flow不同attempt/ref、部分实际n保留在原segment、并发checkpoint守恒。身份、Conn、error和n的注入必须逐项标明；这些检查不增加21个实际连接槽，也不证明网络取消或lazy协议可达。
3. **实际适配器边界**：HTTP adapter使用net.Pipe和显式取消context检查取消返回；其真实调用与注入dialer分开说明。未知外层、透明wrapper、HTTP预读和initial-write边界复用同固定源码已接受的定向检查，说明与本候选的逐字绑定。适配器/Pipe检查不算完整核心TCP实验；已覆盖且代码未变的检查不重复运行以增加数量。
4. **身份、策略和恢复前提**：同hash的T127/T012正式原connector/三值规则边界及T128/T013正式来源/接收恢复继续有效。T129逐流重新核对实际身份/路线/原始来源与接收journal，不借当前group配置猜历史。G05只引用实际离线策略、未知拒绝和有限控制harness证据；不把collector在线等同生产管理器在线，也不把本次替换称为完整管理器接管验收。若任何代码、二进制、profile或原身份路径改变，必须先裁决受影响旧门槛，不能直接沿用其通过标记。

T129提供逐flow/attempt的原始质量样本与可核对线路，T014复核其G03来源定义、起止、失败和历史关联。FR-047/SC-012的生产p50/p95、窗口速率、分组/排序及CLI/API一致性仍由T095–T103等对应任务实现并验收；本次来源可行性不能勾选这些任务，也不以少量准备样本声称已实现完整统计产品。

## 必须保留的断言

- 真实ready仅来自已校准协议完成点。HTTP完整200回复、SOCKS有效完整BND、DIRECT目标TCP建立分别校验；独立扣留/DNS观察只比较同钟域内的时长。源flow/attempt/ready/finish四个差值等式单独核对，不用now、连接存活时间或业务响应时间回填。
- 失败没有返回Conn时，actual_connection_ref和实际链不可伪造。客户端写入入口的字节与代理实际转发字节分开；明确closed 0/0、没有来源终末及gap是三种不同状态。
- 每flow保留全部实际attempt，失败不能被后续成功覆盖；重试中的旧segment不移到成功段。嵌套内层承载外层握手头，其relay字节不能强制与应用33B相等，必须核对实际原文与actual n。
- 完整替换场景必须保留旧A活动流及12B checkpoint/receipt，新B流先完成，旧A再续传；旧core退出后新owner再次成功接纳新流。旧frames/manifest不得改写。display_alias只属于mapping，stable node/group和当时generation关联原连接；没有UI能力结论。
- 每个正式槽位先登记后执行。失败、超时、未执行及缺证据保留在对应分母；可选条件分支未触发只能记未观察，不能补成通过。核心仍可转发也不能掩盖来源缺口。
- 独立复核完成后，主代理分别记录“正式运行观察”和“受限验收决定”，公共只读入口校验已接受证据而不启动VM、核心、网络或writer。T129/T014通过也不自动完成T017/T018或整个P0。

## 明确不由本次证明的能力

客户端断开取消上游dial、同core在线selector写入、同端口无缝切换、真实网络lazy/TFO及未校准代理协议均不获得支持声明。它们不是环境失败，也不以注入用例替代。当前正向网络校准使用明文回环IPv4、inline节点及固定Linux/amd64内核；命名provider、TLS/认证、完整UDP/QUIC、生产降权、SSH/休眠、其他平台、SQL账本和质量界面仍按各自任务验收。

这些边界必须随CapabilityRecord和后续质量展示保留。未来增加实际协议、取消传播或selector控制时，先修改能力实现并固定新制品，再登记相应真实场景；本次分层通过不能用作这些新增能力的证明。
