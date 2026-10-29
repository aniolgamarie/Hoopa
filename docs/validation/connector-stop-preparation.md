# T132 停止写入候选准备

日期：2026-10-23。继续用户已确认的“暂时没有，先完成原型准备”。本轮将“关闭link不能证明停止写入”的问题具体化为单锁准入/计数候选，接入有限观察器。**没有执行真实BPF load、attach或TEST_RUN，没有修改cgroup。** T132仍未勾选，139项/21完成不变。

## 交付与依据

[停止协议契约](../../specs/001-hoopa-proxy-manager/contracts/connector-stop-spike-v1.md)冻结40B gate、原始观察body的包裹范围、控制调用和失败语义。[gated_snapshot.bpf.c](../../experiments/sourceidentity/bpf/gated_snapshot.bpf.c)增加带锁admission/exit和不attach的TC停止程序；控制程序只关闭准入，不写回活动计数、不重开generation。原connect/lifecycle两个body逐字符保持，抽出inline body后由不同profile wrapper调用。

停止者与回调用同一锁排序；用户态仅用BPF_F_LOCK读取。所有观察/fault写在admit与finish之间，见到closed且active归零后才扫描，首尾gate读不一致则拒绝。固定内核实现为同锁release/acquire提供源码依据，具体链接和调用链见契约；没有将这一推理当作本机厂商内核通过证据。

[native_loader.py](../../experiments/sourceidentity/native_loader.py)严格核验新程序和共享map集合，绑定已存在的两个库API；旧TEST_RUN wrapper不限制输出容量，因此使用NULL data_out。会话先停writer再关闭link，任何停止错误都禁止正常扫描。runner仅增加显式gated profile；默认lifecycle和加载开关边界保留。

## 实际验证

| 层次 | 已观察结果 |
| --- | --- |
| 有界模型 | 6项测试通过。1/2/3回调的状态/转换数为17/30、92/239、489/1687；正确模型没有停止后写入反例，两个错误协议均有5步晚写反例，整map写回另有3步丢计数反例。每个body抽象为一次观察或fault写，不是内核内存模型证明 |
| 离线准备器 | 18项假工具测试通过，新增gated三源码提前快照、旧profile兼容及tc section校验，仍只有4条固定编译准备命令 |
| gate加载/会话 | 11项fake CDLL测试通过，真实wrapper与会话贯通；含ABI/计数、u64字符串、精确map集合、禁止attach控制程序、LOCK读取、NULL输出TEST_RUN、延迟排空/双重上限、初始非零/失败/漂移拒绝及清理顺序 |
| 受影响接口 | native 20、session 14、runner 14、解码10、对象检查8项通过；runner新增显式profile选择测试 |
| 最终稳定Python验收 | 8份测试文件共101项顶层测试通过、0失败/跳过，2.583s；不展开subTest，不作为身份样本计数。编译语法另行检查 |
| 实际离线构建 | capture、lifecycle、gated各4步exit0；使用已有clang 20.1.0/bpftool和固定本机5.10 BTF，无依赖安装。生成对象仅保存在ignored private |
| 实际只读对象解析 | libbpf open/read/close三个新对象；capture为1程序/3map，lifecycle为2/5，gated为3/6，声明全部匹配；无load/attach/TEST_RUN |
| 默认程序兼容证据 | 两个观察body与改动前逐字符相同；新旧capture的connect4、lifecycle的connect4/sockops指令section字节逐一相同。整个ELF因源码/BTF调试信息变化而有新hash，不能称原二进制未变或已运行兼容 |

完整日志、对象与源码hash见机器归档（本地原件 `../../experiments/sourceidentity/stop-preparation-validation.json`，不随Git上传）。旧对象、旧验证JSON、001–007补丁及核心证据保持原样；本轮修改了C wrapper和共享准备/加载代码，新结果绑定新摘要，不覆盖旧运行记录。未重跑无变化的正式100流或无关Go矩阵。

## 结果边界和下一步

停止协议候选解决的是“已准入观察body不再写入”的顺序；在途回调可能仍执行返回指令，被拒绝的晚到回调仍会取得锁。它不证明RCU全部退出、TCP全部结束、所有事件均被准入、名称快照正确或原accepted socket唯一关联。旧socket callback flags保留，新attachment影响仍须实测。

writer_stop保存closed_and_drained_observed候选记录，整个结果仍observed_unverified；quiescence/completeness/association保持unverified。capture_fault非零、poison、读错或计数漂移都拒绝成功。默认2秒和1001次只限制TEST_RUN返回后的轮询，不限制同步内核调用或锁等待总时长。

| 状态 | 本轮结论 |
| --- | --- |
| 已完成 | 停止协议契约、正确/错误交错模型、gated程序及加载/会话接线、101项测试、三profile实际离线构建和解析、默认指令兼容核对 |
| 进行中 | T132真实停止与范围/连续性验证、原accepted socket唯一关联及核心身份接线；未放行T137、正式矩阵或P1 |
| 失败待决策 | 旧G01 0/100、首次active异常及旧tuple关联/回收缺口保持；两种被否决的停止方法有模型反例，不是运行身份失败的新分母 |
| 环境不足未验证 | verifier/helper权限、map锁和回调实际交错、TEST_RUN行为、cgroup/PID/netns坐标、停止及资源释放成本、信号/强制退出与内核阻塞行为；本机5.10 BTF编译不替代优先v6.6环境的实际验收 |

下一步在具备授权的隔离Linux环境预登记少量并发正反样本，先验证这个gate和监测范围，再推进accepted关联。原型仍独立于生产路由、账本、CLI/TUI；不把测试通过作为整体项目完成。

实际执行模型：executor为GPT-6.1-sol / medium，独占模型、准备器和新gate测试；scout为GPT-6-luna / high，只读核查固定API、锁与停止调用链、稳定实现。主代理负责协议决策、BPF和loader接线、错误边界、实际离线构建及最终验收；最多两个子任务，无再派生。
