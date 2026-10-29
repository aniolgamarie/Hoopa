# G03 延迟与实际线路：固定source5受限验收通过

2026-10-26。T129及T014已完成，消费同一份[正式质量/线路证据](source5-formal-quality.md)，没有重复采样或修改旧运行。固定0017在六个预登记VM、21原应用槽中全部通过；受限接受与只读入口均已完成，Go race 1通过、0失败、0跳过。原GOST路径的不足保留在下方历史记录。P0/P1仍未放行。

## 当前同源证据

原连接采用connect_initiator_v1，与同一source/epoch/flow/attempt/generation的事件相连；实际Conn产生后记录actual_connection_ref及当时链，tracker binding核对同一引用。失败未返回Conn时保留逻辑选择/unknown，不凭当前group或GOST SID猜线路。两个实际节点、旧新generation并存、展示别名变化、B→A嵌套和失败后成功重试均有原件。

HTTP完整200、SOCKS完整有效BND和DIRECT TCP分别校准ready点。flow从完整目标已知开始，attempt有独立起点；慢DNS两次真实查询区分规则阶段等待与DIRECT dial。各源时间等式、整数毫秒取整和独立观察等待按预登记容差核对，不用存活duration、now或业务RTT填充。HTTP attempt137/126ms对应flow138/127ms，SOCKS attempt138/124ms对应flow140/125ms；DNS flow137/134ms、attempt均取整0ms，有原始纳秒及ready证明。

七条失败连接记录52个真实attempt及52个helper accept，两条stall分别timeout5000/5001ms；客户端写33B与来源closed0/0分别展示。四条nested/retry流保留6 attempts；失败attempt不被后续成功覆盖。全部21 flow有来源终末，14成功流各33/33、7失败流0/0，checkpoint不重复累加。内层relay154/92含外层HTTP121/59B握手，应用边界仍33/33。七份epoch共194原source事件与原接收journal/receipt逐字对应。

F26/F27/F28在本P0阶段证明来源定义、失败分类及原连接历史关联；[ADR011](../adr/011-source5-quality-acceptance.md)把实际网络与source5受控取消/unknown/partial-write、适配器Pipe明确分层。后两层不增加21真实槽，也不证明客户端断开能取消核心dial或真实lazy/TFO。完整替换使用不同端口/核心，不宣称同端口无缝更新或在线selector写入。

FR-045–048及SC-012/013的产品展示、统计p50/p95、同名合并、进程/域名交叉窗口、单位时间流量和排序仍由T095–T103等后续任务实现；业务TTFB/HTTPS内部复用不可见保持N/A。来源可见前缀、完整生命周期及接收ACK也不等于SQL提交。其他协议/provider/平台、生产权限、SSH/休眠仍另验，T017/T018不可由本次受限通过自动完成。

## 2026-10-22 原组合历史记录（结论不改写）

日期：2026-10-22。受控成功、慢响应、失败、超时及长连接已运行；正确的质量事件源和跨跳历史关联仍缺失，T014 保持未勾选。

三路径各 7 项受控校准通过，记录 client_round_trip_ms。它包含客户端请求、代理、目标和收发过程；不是从“完整代理请求已解析、目标已知”到“转发通道 ready”的 forward_setup。HTTPS 正文与连接复用内的业务 TTFB 没有观测点，保持 N/A。100 ms 超时也不作为精确成功耗时。

recorder 20 条记录都有数值 duration，但没有 forwardStart 等足以核实起止的字段。现有材料不能证明 duration 的语义是 forward_setup，不能通过重命名字段或减去猜测时间获得该指标。

Mihomo 活动快照包含它自己的 connection ID/chains，真实 Agent 的实际链为 UPSTREAM；GOST recorder 使用另一 SID。没有验证共同连接标识、可恢复跨跳映射、两节点切组前后的原连接链、改名或一进程多节点分布。不能用当前选择 now 回填历史。

合成 Timing/Route oracle 会拒绝不同定义/目标、错世代/连接及 now 回填；通过这些测试不证明真实采集。F26/F27/F28 的完整分位数/窗口/两线路参考矩阵仍未执行。见 [ADR 001](../adr/001-event-source.md) 的数据源整改契约。

## 重规划路线

按[ADR 005](../adr/005-telemetry-replan.md)，下一原型在同一Mihomo数据面生成flow/attempt身份、setup起止和实际chain，取消两个核心间按时间/端口猜配。stock v1.19.26没有本项目需要的完整事件契约，本轮未构建增强核心，G03仍未关闭。
