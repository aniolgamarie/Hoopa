# TCP Fast Open 与普通半关闭的区分

2026-10-24，固定6.18.54.1内核的独立观察完成。76项准备检查后，一次无NIC、无宿主共享VM `vm-tfo-info-2h8fwuu3` 满足[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-tfo-info-v1.md)，见机器记录（本地原件 `../../experiments/sourceidentity/tfo-info-validation.json`，不随Git上传）。该guest不含BPF或Mihomo。

| 连接 | 原accepted首次state | 最后state | options | TFO_CHILD | 双向各33B |
| --- | --- | --- | --- | --- | --- |
| 普通 | 1 ESTABLISHED | 1 ESTABLISHED | 7 | false | 完成 |
| 普通SHUT_WR | 1 ESTABLISHED | 8 CLOSE_WAIT | 7 | false | EOF后仍收到反向应答 |
| TFO | 1 ESTABLISHED | 1 ESTABLISHED | 167 | true | 完成 |
| TFO SHUT_WR | 1 ESTABLISHED | 8 CLOSE_WAIT | 167 | true | EOF后仍收到反向应答 |

TCP_INFO读取来自原accepted FD，保存原bytes/实际长度/摘要并由host独立解码。options offset5的bit128在两条TFO均存在、普通连接均不存在。四条请求/响应使用预登记token，双方endpoint/cookie、操作顺序、十二个socket关闭与两份导出文件hash全部核对。guest的tcp_fastopen从1设为1543（0x607）后恢复1；listener和TFO客户端的option读回已记录，不以普通握手回退作为TFO成功。

这一观察验证了选择标志的依据，但没有复现early-accept竞态；四条连接在第一次读取时均已ESTABLISHED，也没有测试data-less SYN。不能据此声称新核心已拒绝TFO。

## 对关联设计的影响

早期私人源码笔记曾推断TFO缺少passive-established回调，从而必然缺binding；该推论已撤回并保留纠正记录。固定源码 `tcp_fastopen.c` 在SYN创建child时执行该回调，可能早于客户端active回调；若旧tuple尚存，不能套用普通握手中active先发现复用冲突的有限结论。当前是结构性风险分析，尚无真实错误归属样本。

固定源码在child可被accept前置 `syn_fastopen_child`，`tcp_get_info`将其输出为 `TCPI_OPT_TFO_CHILD=128`。普通连接期间该标志不清除，重置在 `tcp_disconnect`；核心独占原accepted socket、不把FD交给外部。`SYN_DATA`标志不能覆盖无数据SYN的全部TFO child；只要求ESTABLISHED则会误拒合法半关闭。

实验准入代码新增[原FD检查](../../experiments/sourceidentity/coreadmission/tcp_info_linux_amd64.go)，在cookie/map读取及协议IO之前拒绝TFO_CHILD；读取错误和短结构也拒绝，普通CLOSE_WAIT不因状态被拒绝。新原型只接受固定release，launcher另核验kernel image hash；release字符串本身不作为镜像完整性证明。0010补丁已从干净固定上游应用、构建，包级race为20顶层/101子测试、0skip；首轮测试错误地只期待EBADF，沙箱实际先返回EPERM，已保留失败并明确两种读取失败均须传递。

新核心已通过[两协议early-SHUT_WR正向回归](connector-core-tfo-positive.md)，TFO拒绝运行验收仍进行中。旧001–009制品和全部原记录保持，正式identity仍unverified、G01新样本0，T132/T137不勾选。

补充记录：同一0010候选的[独立TFO同四元组拒绝实际回归](connector-core-tfo-negative.md)已完成有限判据核对，见负向机器记录（本地原件 `../../experiments/sourceidentity/core-tfo-negative-validation.json`，不随Git上传）。该后续记录不扩大本页原实验的证明范围，也不识别具体内部拒绝分支。
