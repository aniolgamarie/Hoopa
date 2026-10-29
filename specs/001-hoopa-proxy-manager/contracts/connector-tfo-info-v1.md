# 固定内核 TCP_INFO 的 TFO child 观察契约

状态：2026-10-24 预登记；仅隔离 Linux 6.18.54.1 guest 的独立观察，未运行，不是核心修复验收。

固定内核 SHA256 `b3dc3a9c3774fda2b9d7bceac7495248df50a6e0cd4ba9596ef98933f384d495`。固定源码 commit `d504d40ab83839cb61942a812a99d183c42227b9` 中 `tcp_fastopen.c` 在 SYN 创建 child 时执行 passive-established 回调；可能早于客户端 active-established。普通三次握手的 tuple 复用拒绝不能直接覆盖此时序。

`TCP_INFO` 的 `tcpi_options` 为 offset5，`TCPI_OPT_TFO_CHILD` 为 bit128，由 `syn_fastopen_child` 导出。`TCPI_OPT_SYN_DATA` 不能识别无数据 SYN 的全部 TFO child；也不能以 `TCP_ESTABLISHED` 单一状态判断，因为正常客户端半关闭会使服务端为 `CLOSE_WAIT`。

仅在 PID1、root、`hoopa_lab=t132` 的独占 guest 中允许设置回环接口、私有端口及 TFO sysctl/socket options。无 NIC、无宿主共享，不挂 BPF，不启动代理核心。以明确记录的 guest TFO 配置确保真实 TFO，回退普通握手不得作为 TFO 成功。

必须观察四项：普通连接、普通连接客户端 SHUT_WR、TFO 连接、TFO 连接客户端 SHUT_WR。对原始 accepted socket 保存 TCP_INFO 原始 bytes、返回长度、state/options，以及两端地址、cookie、请求/应答原字节、操作顺序。普通两项必须无 TFO_CHILD；TFO 两项必须有 TFO_CHILD。半关闭项必须确认服务端 EOF 和 CLOSE_WAIT，仍能反向发送应答；不得将整个 socket 关闭代替半关闭。

host 独立解析原始字节与状态，核对正反断言、产物 hash、内核/夹具 pin、导出完整性、guest 故障与清理。保存 sysctl 修改前/后/恢复值。任何不符合都保留本次失败，不事后改预期使同一 run 通过。全部通过也只证明固定内核这四个可达样本的区分能力，不证明早期 accept 竞态已复现、data-less TFO、核心准入拒绝或跨内核 ABI 兼容。
