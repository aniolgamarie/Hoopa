# US3 逻辑组静态解析（T040）

2026-10-28 验收纯组计算。manual只接受精确来源+节点ID；auto保留确认来源/地区内全部候选及probe配置，Selected为空，不伪造最佳节点；follow保留自己的稳定ID并复制父组完整候选、选择与拒绝状态，不另选或缩窄。活动来源切换统一影响US/JP/BEST及跟随链，显式固定来源保持原pin。缺失引用、无效pin、环、必需空组均拒绝整份结果；可选空auto及其可选follow明确Reject，不转直连。

仅验证被引用来源的元数据形状；本纯函数可接受测试合成Snapshot，但不赋予私有材料真实性。T041必须另外验证Snapshot.Validate与完整来源资产。unknown/ambiguous地区不能补充受约束候选。输入及不同输出组间不共享可变slice/pointer；DAG按迭代顺序解析，20,000层链与深环都有测试。

固定Go1.27.3/offline：policy普通/race各73PASS、0FAIL/0SKIP（15个顶层测试，含原T036六组）；policy/config vet通过。typed Probe.Validate复用严格URL与安全整数/Go持续时间边界。root审查稳定实现与输入hash，没有运行网络、核心或probe；原件在private/us3-preparation/t040/。executor实际gpt-6.1-sol/medium，无再派生。

下一步T041还必须扩展并验证受保护核心协议：现basic-v1仅支持loopback HTTP/SOCKS和Selector，不能把普通-t成功视作自动组或真实远端在protected模式可用。该缺口不改变本静态组函数验收，也不代表US3/项目完成。
