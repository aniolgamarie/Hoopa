# G02 累计与终末计数：受限 source5 验收通过

2026-10-26。T013消费已接受的T128同一批正式run完成评审，不新增VM或变更分母。[Source5正式恢复报告](source5-formal-recovery.md)记录8VM/16槽全部通过；[验收清单](../../experiments/mihomo-events/source5-recovery-acceptance.json)固定0017及全部原始材料，真实总入口（本地原件 `../../experiments/mihomo-events/source5-recovery-gate-validation.json`，不随Git上传）race1通过、0失败、0跳过。结论限来源spool和接收journal，P0/P1仍未放行。

- 同源身份以source_instance/epoch/flow及generation绑定；source_seq是来源位置，receipt_seq是去重后的接收位置，delivery_index是投递次数。三者不互换。
- 93条完整原帧、14条真实source flow分别核验累计单调和连接段守恒；12条有精确终末，2条只保留可信12/12检查点和null final/tail。16个应用槽中的另外两条在计量准入时被拒绝，没有来源流，不编造closed0/0。
- 完整终末按累计减最后已结算累计产生delta；TERM与配额第一流都是12−12=0。原文重放保留同receipt，异原文冲突409无新receipt，不以接收次数重复相加。此处验证来源/接收事实，生产结算事务仍由T019、T069/T071/T073承担。
- 离线、接收前503、持久接收后丢ACK及阻塞ACK均使用同一来源原文补投；重开collector保持旧receipt前缀。核心新epoch、新连接与旧流历史分别保存。
- 旧核心退出后的只读结果仍pending；固定代码对实际输入私有副本执行三次独占writer重开，才产生稳定派生gap，保留CP且final/tail=null。真实满盘8192B原件含92B坏尾，三次副本重开拒绝、不修复。应用实际17/17不能回填来源可信12/12的终末。
- 字节边界是已定义的代理stream：buffer每流33up/41down，target33/33，额外8字节属于节点响应。固定代码的整数超过2^53及Write/Sync边界另属合成/注入验证；GOST浮点series不得拿来精确结算或跨口径求差。

F05/F06/F16/F24按上述正式矩阵关闭本任务的受限来源/接收子范围。真实ENOSPC的complete尾成功恢复、TERM另一gap分支、实际非空客户端预读未在VM观察；对应固定代码分支的合成覆盖单列。SQL事务、生产回收、性能及其他平台不由本次通过推导。下面保留旧GOST候选的完整标签、计量边界及重投失败历史，既不改判，也不混入source5分母。

## 2026-10-22 GOST 候选历史：未通过完整G02

日期：2026-10-22。受控 payload、精确解析和固定 series 差分子断言通过；终末事实、入口边界及恢复保证没有完成，T013 保持未勾选。

[flow_test.go](../../experiments/gost-mihomo/flow_test.go) 覆盖完整标签归一、重复 series、世代变化、消失/新增/下降、大数、非整数和边界。每条 Prometheus 原值以精确有理数解析；`>=2^53` 的浮点来源计数不可作为逐字节精确事实。求和为溢出检查后的整数字节，JSON 用十进制字符串，缺方向/世代冲突保留 null/reason。

首轮真实校准 before/after 各有 2 条 input、2 条 output，标签名为 client/host/service；完整标签集合未变化。差分汇总 input `72198`、output `466001`。这不是两个方向的 payload 参照值，不隐去 HTTP/CONNECT/TLS 或采样截止差异。

recorder 实验见 `/tmp/hoopa-p0-3014871418` 与脱敏摘要（本地原件 `p0-2026-10-22.summary.json`，不随Git上传）。20 条 decoded 记录都包含可精确读取的整数 inputBytes/outputBytes；首个合法投递返回 503，其余回复成功。20 个 SID 不重复，因此在本轮校准及追加 1 秒收尾窗口中，没有观察到首条 SID 再次投递。不能由此断言任意长时间均无重试，也不能证明持久序号、重放和崩溃后恢复。

该 run 的 metrics 差分为 input `72202`、output `466001`；recorder 收集还覆盖启动/认证检查及不同终末截止时间，不能直接与上述差分同口径相减。入口与终末计数尚无逐连接、相同方向/截止点的对账证据。所有差异保持 not_comparable，没有比例分配或任意容差。

当前 run ID 仅在同一个已启动 GOST 全程存活、不进行重载/替换时充当实验采样 epoch；不是生产计数世代协议。F05/F06/F16/F24 的实际重启、重载、停采恢复、terminal cutoff 与开销拆分尚未完成。未编写生产账本，不存在可据此声称通过的 SQLite checkpoint/delta/gap 事务。

## 重规划后的追加证据

[接收journal与真实recorder接线](telemetry-replan.md)已验证20已送达事件的Write+Sync、落盘后ACK503及重开逐记录一致。它关闭已送达接收持久化子断言，原生来源未送达、计数边界与SQL事务仍待验；receipt_seq不是source_seq，G02保持未关闭。
