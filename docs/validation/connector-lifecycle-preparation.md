# T132 socket 生命周期原型准备记录

日期：2026-10-23。接续[首次捕获准备](connector-preparation.md)，按用户“暂时没有，先完成原型准备”继续。本次实现了可离线编译的生命周期观测目标，**没有运行 BPF 或完成唯一进程归属，T132 仍未完成**。总任务139项、完成21项；旧G01的0/100不改。

## 本次变化

[生命周期源码](../../experiments/sourceidentity/bpf/lifecycle_snapshot.bpf.c)包含原capture源码，共用配置、connect记录和故障map；增加按socket cookie保存的48B established及40B TCP_CLOSE观察。active回调查原connect cookie及generation/cgroup/netns，passive只记录端点和自身cookie，不推namespace或connector。状态通知保留已有flags，错误使本轮失效；两张新map仅插入、不覆盖、不删除，有限容量不回收。它没有tuple索引、accepted→connector接口或核心路由接线。

[准备器](../../experiments/sourceidentity/prepare.py)增加 `--profile lifecycle`；默认capture保持原目标。lifecycle的两份固定C输入在工具启动前快照，manifest逐文件绑定摘要，ELF增加sockops section检查；不引入任意源码输入、loader或下载路径。原始capture C和旧机器证据不变，新对象只在ignored private目录保存，不提交二进制。

精确ABI、回调含义、停止条件与后续运行顺序见[新契约](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md)。TCP_CLOSE不代表应用退出、socket销毁或可安全删除关联；回调时间差不是T134的协议ready延迟，缺close记录也不能说明连接仍活跃。

## 修正的设计前提

固定上游v5.10的sockops分派不支持get_netns_cookie；sock_addr中的同名helper可用性不能转移给sockops。该版本没有SO_NETNS_COOKIE选项，SO_COOKIE也不能代替namespace证据。本机厂商实现没有完整源码或运行验证，本次不由上游阅读宣称其具体verifier结果。[固定v5.10 helper分派](https://github.com/torvalds/linux/blob/v5.10/net/core/filter.c)、[socket选项](https://github.com/torvalds/linux/blob/v5.10/include/uapi/asm-generic/socket.h)。

未来关联实验首选固定v6.6验证这两个netns-cookie接口，仍须登记实际内核/BTF与权限、验证helper和原fd；没有要求当前宿主升级，也没有宣布产品最低支持版本。[v6.6 helper分派](https://github.com/torvalds/linux/blob/v6.6/net/core/filter.c)、[getsockopt实现](https://github.com/torvalds/linux/blob/v6.6/net/core/sock.c)。

同时修正“核对旧cookie后delete(tuple)”的错误前提：新连接可在两步之间发布同key，旧关闭处理随后误删新记录。此为API语义推导的反例，未做内核竞态复现；不能用持锁调用map helper作为现成修复。本观测对象不建立tuple索引，完整关联算法及复用仍待单独证明。[v5.10 map/spinlock契约](https://github.com/torvalds/linux/blob/v5.10/include/uapi/linux/bpf.h)。ADR007/008、plan/tasks和执行入口已同步这些限制。

## 验证结果

| 检查 | 观察结果 |
| --- | --- |
| 准备器测试 | 16项通过，0失败/跳过；包括默认兼容、非法profile/任意source拒绝、双源码在工具前快照且变更不影响编译、缺失/超限第二输入、缺失/畸形sockops section及原超时/上限/权限边界。使用假工具，不是身份样本 |
| Python语法 | prepare.py/test_prepare.py 的py_compile通过 |
| 真实离线构建 | clang版本查询、bpftool版本查询、固定BTF导出、clang bpfel编译四步exit0；clang20.1.0、bpftool v5.10.134-17.2.al8.x86_64；本机BTF来自5.10.134-17.2.al8.x86_64 |
| 对象结构 | 659504字节，ELF64 little-endian EM_BPF/ET_REL，非空connect4/sockops/maps/BTF/BTF.ext/license；编译期48B/40B ABI断言通过；SHA256 `92c5052169b195977f715c893da787017db8dca681b7d50ba13d262a7d20386e` |
| 对象静态检查 | sockops helper集合为map_lookup/update、get_socket_cookie、sock_ops_cb_flags_set、ktime_get_boot_ns；无netns-cookie调用；两程序均无map_delete调用。源码快照与最终C一致。这只是反汇编/文件一致性检查 |
| 独立源码复核 | scout核对固定UAPI端序、STATE_CB参数、flags返回语义、map插入限制和passive不推namespace；未发现新增阻断错误。没有执行内核程序 |

原始日志、对象、BTF/C快照和manifest在 `private/t132-lifecycle-preparation/hoopa-sourceidentity-lifecycle-52iz6olv/`；机器归档（本地原件 `../../experiments/sourceidentity/lifecycle-prepare-validation.json`，不随Git上传）绑定最终源码和证据摘要。准备器单元测试已覆盖本次变更；本轮没有改Go代码，不重复旧proc holder或正式100流测试，也没有新Mihomo补丁/核心。

## 四类状态与下一步

| 状态 | 当前结论 |
| --- | --- |
| 已完成 | 生命周期观察源码、profile/双源码快照、16项准备器测试、真实离线编译与静态检查；helper和非原子删除前提修订 |
| 进行中 | 有限loader/读取端、namespace证明、原accepted cookie关联算法、复用/早退/连续性及核心接线。当前观察不等于A/B原型运行验收 |
| 失败待决策 | 原G01 0/100与首次active异常保留；旧tuple清理假设已否决，完整安全关联算法仍未通过。本次没有新增运行身份分母 |
| 环境不足未验证 | verifier/load/attach、真实回调及原fd核验、故障覆盖/停止读取、进程名称一致性、跨namespace与性能；用户暂无隔离环境，本轮未尝试提权或改变系统 |

后续先按契约在隔离环境实现并审查有限loader，验证两程序共享map、配置freeze和覆盖范围，然后核对客户端/服务端各自cookie与真实生命周期。唯一关联及其反例通过后才接核心路由和T137；16项单元测试和prepared对象不能关闭T132或放行正式矩阵/P1。

executor实际角色配置为GPT-6.1-sol / medium，独占prepare.py/test_prepare.py；scout为GPT-6-luna / high，独立只读核实上游和稳定源码。主代理负责关键生命周期语义、C源码、方案修订、真实编译及最终验收；最多两个子任务，没有再派生。
