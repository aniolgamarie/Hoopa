# T132 有限 loader 与会话控制准备

日期：2026-10-23。继续用户已确认的“暂时没有，先完成原型准备”。本轮实现有限观察器的加载封装、资源清理和显式实验命令；**所有load/attach路径仅通过假接口验证，没有执行真实BPF加载或cgroup修改。** T132未勾选，139项任务完成21项；旧G01 0/100和首次active异常继续保留。

## 实现范围

- [native_loader.py](../../experiments/sourceidentity/native_loader.py)：调用固定libbpf接口，核对对象声明与加载后map/program info、两个程序共享的map ID集合、配置初始化/readback/freeze、直接BPF_LINK_CREATE及link info、有界map枚举。没有legacy attach回退、pin或运行中回收。
- [loader_session.py](../../experiments/sourceidentity/loader_session.py)：仅在全部挂接核验成功后发出attached_observer；预留两份FD所有权位置，挂接返回后先登记，再核验。失败逆序清理、每个FD只关闭一次，保留原始错误和清理错误；信号由runner转为延迟停止请求。
- [run_observer.py](../../experiments/sourceidentity/run_observer.py)：默认在读取输入前拒绝加载，显式开关之外还须提供固定对象/库摘要、已有空cgroup v2叶目录、预登记配置和私有输出位置。它不创建cgroup、搬移进程或启动测试客户端。0–60秒观察窗口不等于内核调用总超时。

FFI使用固定[libbpf v0.6.0 libbpf.h](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf.h)与[bpf.h](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/bpf.h)作依据，本机只读符号检查确认所需导出。两个program setter返回void，随后getter校验；不能因本机库文件名带0.6.0便声称它等同上游tag或运行兼容已通过。没有安装依赖。

## 停止语义的修正

关闭cgroup link会更新effective程序数组；在途回调仍可能经旧RCU数组执行并写map。MAP_FREEZE仅限制用户态写入，不能阻止BPF程序写观察map。依据固定[v6.6 cgroup实现](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/cgroup.c)和[syscall实现](https://github.com/torvalds/linux/blob/v6.6/kernel/bpf/syscall.c)，当前无法从这两个操作推导稳定快照。

因此loader只输出逐map的diagnostic_snapshot，quiescence/completeness/association始终unverified；零fault、相等扫描或空map均不代表完备。正常流程返回observed_unverified/exit0，不能生成passed CapabilityRecord。读取、非零fault、解码或清理失败返回exit1；缺失快照为null，解码失败保留原key/value hex。uint64包括link的cgroup ID都用十进制字符串。

socket上的STATE_CB_FLAG不独立持有已detach程序，但flag会保留，后续新attachment可能收到旧socket的状态通知。这来自固定[flags helper](https://github.com/torvalds/linux/blob/v6.6/net/core/filter.c)、[TCP状态变化](https://github.com/torvalds/linux/blob/v6.6/net/ipv4/tcp.c)与cgroup调度路径的源码推导，未在当前厂商内核验证。清理FD不声称已恢复全部socket状态；未来稳定停止和复用实验须覆盖该边界。

## 实际验证

| 层次 | 结果与边界 |
| --- | --- |
| NativeBackend | 20项fake CDLL测试通过。真实Python wrapper调用假符号，覆盖void setter、信息长度/ID/引用不符、FD 0、freeze/readback及写入拒绝、容量/重复key/errno、无fallback、单次close、u64字符串及登记失败回收 |
| 会话控制 | 14项测试通过。逐阶段故障、部分挂接、中断、清理多重错误、目标变化、解码/fault失败及控制器接真实wrapper/fake CDLL的贯通用例；没有真实内核调用 |
| 实验命令 | 13项测试通过。未opt-in提前拒绝、平台/范围/ELF/路径、假cgroup准入、0700/0600输出、signal恢复和cgroup FD关闭；会话、构造或复核同时出错时保留各自错误。CDLL、内核输入与BPF边界均fake |
| 既有对象/解码接口 | 8项假库对象检查和10项纯bytes解码回归通过；不重跑历史真实对象检查或无关Go/正式100流 |
| 语法与静态检查 | 六个新Python文件语法通过；变更文档本地引用、任务计数、历史输入摘要另存静态验收 |

本轮新增47项测试，加受影响接口回归18项，共65项顶层测试通过、0失败/跳过。计数不展开subTest，也不是身份样本分母。日志、稳定源码和历史输入摘要由机器记录（本地原件 `../../experiments/sourceidentity/loader-session-validation.json`，不随Git上传）绑定，放在ignored `private/t132-loader-session/`；没有把二进制或运行记录提交到Git。

贯通测试实际发现map ID证据被close_object清空：返回结果和内部缓存曾共用同一dict，最终输出会丢失核验信息。已改为独立副本，修前13通过/1失败日志保留，14项回归通过。独立复核另确认大cgroup ID序列化和FD返回后登记分配失败的边界，已用字符串及登记失败单次回收补齐测试；不能据此承诺进程被SIGKILL、内核故障或全局OOM时仍能写完报告。静态发现的会话主错误被库复核错误覆盖也已修复，有三条双错误回归。

## 当前状态和下一步

| 状态 | 事实与边界 |
| --- | --- |
| 已完成 | 有限lifecycle加载封装、会话顺序、默认拒绝的实验命令和上述假接口/纯bytes检查；旧C、准备器、对象检查/解码源码、验证JSON与001–007证据未改 |
| 进行中 | T132的停止同步/稳定map边界、完整监测范围、原accepted cookie唯一关联与核心身份接线；有限观察器尚不能完成这些证明 |
| 失败待决策 | 旧G01 0/100和首次active异常保持，尚无新真实身份成功结果；旧tuple复用/清理算法不因本轮代码准备而成立 |
| 环境不足未验证 | 实际CO-RE/verifier、map共享/freeze/attach、PID/netns/cgroup坐标、真实回调连续性、原accepted关联、退出时内核资源回收和CPU/RSS/map成本。未提供隔离环境，没有执行启用加载的命令 |

下一步先冻结停止与范围证明的可执行实验，再在具备授权的隔离Linux环境做小规模正反样本；取得原accepted socket唯一关联和连续性证据后才推进T132/T137及正式矩阵。保持T018/P1门槛，不重复旧正式100流。

实际分工：executor按角色配置GPT-6.1-sol / medium独占NativeBackend及测试、随后独占CLI测试；scout按GPT-6-luna / high只读核验固定API/停止调用链和稳定代码。主代理负责控制器、CLI、信号/清理与证据保留语义、集成反例和最终验收。最多两个子任务，无再派生。
