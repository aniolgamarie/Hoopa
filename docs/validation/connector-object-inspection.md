# T132 对象检查与单条记录解码准备

日期：2026-10-23。继续用户已确认的“暂时没有，先完成原型准备”。本次补齐加载前的实际对象检查和map ABI解码，不包含完整loader或唯一关联。T132保持未勾选，139项任务完成21项，旧G01 0/100及此前运行失败不改。

## 实现与依赖

[inspect_object.py](../../experiments/sourceidentity/inspect_object.py)通过ctypes使用固定摘要的libbpf运行库，实际执行open_mem、metadata读取及close；不绑定load/attach/pin API。对象从已校验摘要的内存缓冲解析，库调用前后校验摘要；拒绝非普通文件、超过64MiB、错误ELF头、异常遍历以及profile声明不符。输入先检查文件类型，打开时用O_NOFOLLOW/O_NONBLOCK并再查类型，避免FIFO造成阻塞。不是针对同UID恶意替换或全部动态库依赖的强隔离。

本机没有 `/usr/include/bpf`、libbpf开发链接/pkg-config元数据，也没有在指定Go缓存范围找到cilium/ebpf或libbpfgo。现有运行库归属Alibaba发行包 `libbpf-0.6.0-1.2.al8.x86_64`，文件名不能证明与上游v0.6.0 tag一致；实际缺新迭代器，使用导出的旧接口。固定库SHA256为 `dcb0380081532521e388ee9e3e6a6ee13340897e8327fd935b2a705c9b8df1ef`。没有安装或下载依赖。

FFI原型参考[固定公开头文件](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf.h)及[legacy声明](https://raw.githubusercontent.com/libbpf/libbpf/v0.6.0/src/libbpf_legacy.h)，并通过本机符号和真实调用核验。独立复核指出map type返回enum，已改用c_int，尺寸/flags用uint32、错误码用long；旧迭代器按previous/object顺序调用。NULL/ERR_PTR不关闭无效对象，有效对象在异常路径同样关闭。

[observations.py](../../experiments/sourceidentity/observations.py)只解码调用方提供的bytes，严格验证配置、ABI长度、key/cookie、generation、schema/reserved及单条可核对的进程/端点字段。uint64输出十进制字符串；comm同时保留16B原文hex、未知状态及可用的UTF-8观察。它不访问内核map、不做集合完整性检查、不根据tuple/时间建立关联，也不从TCP_CLOSE推出进程退出或清理许可。

## 实际验证

| 验证层 | 结果与边界 |
| --- | --- |
| 解码器 | 10项纯bytes测试通过、0失败/跳过；含大于2^53及uint64最大值、混合字节序、PID/leader及namespace/generation拒绝、comm与状态边界。没有真实运行捕获数据 |
| FFI与对象准入 | 8项假库测试通过、0失败/跳过；实际经过Libbpf wrapper，覆盖新/旧迭代参数、精确C类型、无效指针、异常close、遍历上限、map/program不符、hash/ELF/非普通文件拒绝。假库不提供load/attach/pin接口 |
| Python语法 | 新增实现与测试的py_compile通过 |
| capture真实对象 | 既有对象SHA256 `cdd2320131856014032c944058d5d571f29dbcf428ca2e57bb467e1b3f6118ce` 被实际libbpf打开，1个程序/3个map声明匹配，exit0 |
| lifecycle真实对象 | 既有对象SHA256 `92c5052169b195977f715c893da787017db8dca681b7d50ba13d262a7d20386e` 被实际libbpf打开，2个程序/5个map声明匹配，exit0 |
| 畸形对象反例 | 保留合法ELF头但截断为64B的独立fixture被真实libbpf拒绝，exit1、原始错误码-4001；这是预期拒绝，不是运行身份通过 |
| syscall观察 | 固定最终源码快照的三个子进程均用strace -f记录，未见bpf、mount/umount、unshare/setns；可写open flag仅见/dev/null的O_RDWR。filter未包含全部write(2)，结论限于上述系统调用及文件打开/路径操作，不声称捕获了所有写调用 |

默认沙箱中strace因PTRACE_TRACEME/PTRACE_SETOPTIONS被拒，未开始有效对象检查，原日志保留。随后经工具自动审批，仅在沙箱外跟踪本次自有离线检查进程；没有BPF加载、cgroup或系统网络变更。普通测试fixture首次误用17B comm、FFI测试首次把expected_attach_type getter误当attach操作，均修正并保留说明；未改判为产品失败或身份通过。

最终源码快照、三例命令/JSON/stderr/syscall日志、输入摘要及测试记录由机器归档（本地原件 `../../experiments/sourceidentity/object-inspection-validation.json`，不随Git上传）绑定，保存在ignored `private/t132-loader-preparation/`。前面的真实检查与签名/输入修订前结果保留为阶段记录；最终验收使用稳定源码。旧C、prepare.py及旧验证JSON/对象、001–007补丁与核心记录没有修改。本轮未重跑无关Go/正式100流测试。

## 当前状态

| 状态 | 结论 |
| --- | --- |
| 已完成 | 真实libbpf离线对象准入、严格单条ABI解码、18项定向测试与最终三例真实解析/拒绝，依赖和接口兼容性记录 |
| 进行中 | T132有限loader的配置写入/freeze、共享map与全部attach、停止/稳定读取、namespace证明、唯一关联与核心接线；尚未实现完整loader |
| 失败待决策 | 原G01 0/100与首次active异常保留；tuple复用/清理和完整关联算法仍未通过。当前没有新运行身份分母 |
| 环境不足未验证 | verifier/CO-RE加载重定位、attach、真实map故障/完整性、进程名称一致性、原accepted fd关联和资源成本；program type getter结果亦不升级为运行能力 |

下一步仍需在隔离环境中实现和审查有限loader，核验配置freeze、共享map和覆盖范围后放行预登记样本。离线status=inspected不能生成passed CapabilityRecord，也不能放行T137、正式矩阵或P1。

executor使用角色配置GPT-6.1-sol / medium，负责解码器和两组定向测试；scout使用GPT-6-luna / high，只读盘点依赖、固定API和稳定源码。主代理负责FFI实现、关键输入/生命周期决策、真实检查和最终验收，未派生更多子代理。
