# 原始入口身份实验

> 运行报告与准备/诊断过程材料保留在本地原位置，不随 Git 同步。“本地原件”代码路径不是源码 clone 文件；正式 acceptance、构建身份与源码保留。只读验收 gate 仍需完整且 hash 匹配的本地报告/私有原件，不能仅凭本页结论宣称重新验收通过。

2026-10-24 最新状态：用户已选择 `connect_initiator_v1`。继[隔离两流校准](../../docs/validation/isolated-linux-lab.md)之后，已新增 `online` profile 和原 socket 准入服务，取得真实允许、停止/撤销/范围拒绝、线程、父connect/子写、SCM_RIGHTS和同tuple复用证据。详见[在线实验验收](../../docs/validation/connector-online.md)和机器摘要（本地原件 `online-validation.json`，不随Git上传）。隔离环境已可用，实际为6.18.54.1，首选6.6仍未验；此前SIGTERM预登记失配和SIGKILL后诊断fixture仍通信的事实保留，不由新服务的结果改判。

此前online阶段的profile及受影响回归130项、准入服务9项、独立client19项测试通过。其有限VM运行只证明登记范围内的在线关联与拒绝；后续Mihomo有限接线已实测，生产支持与完整身份仍未认证，全部identity_capability保持unverified。T132/T137保持未完成，139项任务完成21项，正式G01的0/100保留。下文“准备阶段”保留当时范围，不表示仍缺少实验环境；历史proc holder探针不能代替connector证明。

## 有限核心接线与最终候选

[`exec_core.py`](exec_core.py)在隔离 guest 中完成固定对象加载/核验后，以同一 PID exec 已核验摘要的核心，移交 6 map、3 program、2 link 共 11 个 FD；Go [`coreadmission/`](coreadmission/owner.go)核验 namespace、配置、对象关系与原 accepted socket，并恢复 FD 的 CLOEXEC。原 Accept 后、协议读取前的 Admit 值经各自复制的 metadata 进入同一 flow 的严格 v3 opened；原帧身份与旧 process unknown、正式能力 false 并存，不开放 PROCESS/UID 规则。

真实最终候选已核对 HTTP CONNECT/SOCKS5 baseline 及原 early-half-close 请求的双向各 33 B、旧来源文件重开 hash 不变与两世代、错 generation/缺 map FD/错 program FD 的零 source/零 listener 拒绝。后续 drain、范围外和核心退出等场景以[核心验收](../../docs/validation/connector-core.md)及机器摘要（本地原件 `core-validation.json`，不随Git上传）各自记录为准，不在此合并计数。0009 独立修复 Hoopa 计量 wrapper 漏传 CloseWrite 的真实缺陷，不开放绕过计数的读写 fastpath；早期失败不改判。

源码固定 Mihomo v1.19.26、commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`，最终 001–009 binary SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`。fresh apply/整树与权限核对、无 ldflags 离线构建见[manifest](../mihomo-events/core-connector-candidate-manifest.json)；打印的默认版本 `1.10.0` 不标识源码。字段与时钟边界见[v3 契约](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v3.md)和[移交契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-handoff-v1.md)。

上述是有限核心接线，未认证完整 scope、跨 namespace、exec/名称竞态、长期 map 回收、生产权限部署或故障接管。comm 仍为非原子 unverified 快照，路径 unknown；T136 管理器退出/接管不能由核心 SIGKILL 结果代替。T132/T137、139/21 和正式 G01 新增 0/旧 0/100 保持原状。

后续新增 `core_test_client.py` / `test_core_test_client.py`，在同一固定核心上完成线程、父建连子写、SCM转交、受控改名和原socket跨exec的五次VM/10流/65帧。见[边界验收](../../docs/validation/connector-core-edges.md)与独立机器记录（本地原件 `core-edges-validation.json`，不随Git上传）。受控顺序改名/exec不证明并发名称原子性。活动来源crash另按[契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-crash-v1.md)完成真实单VM/两流/13帧，核心内部恢复探针完成三次重开；只读pending与持锁writer的gap明确分列，见[验收](../../docs/validation/connector-core-crash.md)和机器记录（本地原件 `core-crash-validation.json`，不随Git上传）。上述client是guest-only测试oracle，不是生产身份helper或规则实现。

## 有限在线准入实验

[`bpf/online_snapshot.bpf.c`](bpf/online_snapshot.bpf.c)复用connect捕获与停止gate，增加只插入的tuple→client cookie及accepted cookie→client cookie绑定。两个link挂到独立guest的cgroup2根，source配置限定专用叶目录；目标connect不满足范围或捕获条件时直接拒绝。与旧诊断profile不同，online不得通过snapshot接口枚举后猜配。

[`online_admission.py`](online_admission.py)在同一进程持有listener、link/map和原accepted socket，授权前不读token；`online_lookup`只按原cookie读取冻结配置、gate/fault、不可变绑定及C记录。缺记录、冲突或停止拒绝并关闭新连接。进程退出终止这个实验入口；它不是Hoopa管理器，也没有验证管理器退出后Mihomo继续转发。

[`online_test_client.py`](online_test_client.py)是独立验收oracle，支持plain、thread、fork-writer、scm-writer及显式source-port。原始PID/start ticks、leader comm、TID、cookie与端点只用于事后核对，不参与服务端身份查询。名称采样非原子，路径unknown，FD转交不能改写原connector。

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_online_profile.py' -v
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_online_admission.py' -v
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_online_test_client.py' -v
```

这些单测不加载BPF、不创建真实网络socket；client测试包含无网络原生线程。实际命令必须显式开启加载/loopback，满足专用guest命令行、初始namespace和空cgroup等条件，并固定对象/库摘要。运行方式与ABI见[在线契约](../../specs/001-hoopa-proxy-manager/contracts/connector-online-spike-v1.md)。此处online_admission服务仍只回显33B token，不转发代理协议、不生成passed CapabilityRecord；后续有限exec移交及Mihomo转发见上节，长期map回收、生产权限部署与故障接管仍待验。

## 小规模校准工具与准备阶段

[`namespaced`](bpf/namespaced_snapshot.bpf.c)复用gated停止协议，established记录单独升级为56B/schema2；active与passive均从实际socket读取netns cookie，非零且须匹配配置。其他观察ABI不变，旧profile不自动升级。离线准备/检查命令使用`--profile namespaced`；真实runner同样须显式指定profile，默认仍拒绝加载。

[`fixture/`](fixture/main.go)提供两个独立角色：服务端自己Accept并绑定原FD见证，客户端从原TCPConn读取cookie/namespace、PID及proc start ticks。双方在真实连接上交换独立token。默认无`--allow-loopback`拒绝socket；仅IPv4 loopback，不加载BPF、不移动cgroup。server记录在全部连接和listener关闭成功后输出；外部实验仍须保存退出码、stderr及挂接/放置时序。

[`calibration.py`](calibration.py)只读预登记、observer input/result及fixture JSONL。它先从原始map bytes建立候选，再按传输token核对客户端cookie与原accepted witness；拒绝缺事件、错见证、重复tuple、namespace/PID矛盾和缺listening事件。`consistent_unverified`/exit0仅表示诊断一致，start ticks与BPF启动ns未换算，完整性与身份能力均未认证。

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_calibration.py' -v
GOTOOLCHAIN=local GOPROXY=off go test -race -count=1 ./experiments/sourceidentity/fixture
```

使用已核验的本地Go及私有TMPDIR/GOTMPDIR/GOCACHE。上述测试只用合成bytes/假接口，不运行socket；两个独立客户端、预登记格式、命令模板及真实阶段顺序见[校准契约](../../specs/001-hoopa-proxy-manager/contracts/connector-calibration-v1.md)。本机此前NETNS getsockopt不支持的结果仍保留，不反复重试它来制造进展。

## T132 捕获子步骤与离线准备（历史阶段）

connect ABI、作用范围、失败规则及待执行用例见[捕获契约](../../specs/001-hoopa-proxy-manager/contracts/connector-capture-spike-v1.md)，原结果见[捕获准备记录](../../docs/validation/connector-preparation.md)。新增目标在同一对象共享 connect map，active 回调按 cookie 查捕获记录，另记录 passive 和 TCP_CLOSE；这仍不是 spike B 的原 accepted socket 关联。该准备阶段的有限loader仅进入假接口验证，当时核心身份接线尚未实现；后续实测见页首。

程序分开保存 leader TGID/start 与调用线程 TID，只接受初始 PID namespace、一个精确 cgroup 和 netns 中发向固定 IPv4 loopback TCP 入口的 connect 调用。comm 仅保存为 unverified 观察，路径不支持。有限 map 不覆盖/驱逐记录，重复 cookie 或捕获错误使本轮不能通过；所有路径放行原 connect，不改变系统代理或连接策略。

使用本机已存在的工具离线准备（不下载，不需要 BPF 加载权限）：

```sh
python3 experiments/sourceidentity/prepare.py \
  --profile capture \
  --clang /data/1/weixiaoxian.wxx/dev_tool/llvm-install/bin/clang \
  --bpftool /sbin/bpftool \
  --btf /sys/kernel/btf/vmlinux \
  --output-parent /data/1/weixiaoxian.wxx/dev_tool/Hoopa/private/t132-preparation
```

`--output-parent` 必须是已有目录；在其他环境选择自己的私有输出位置。每次新建独立 0700 子目录，不覆盖旧制品；源码/BTF先复制为有界 0600 快照，工具仅消费快照，manifest 记录所有命令、输入及产物摘要。运行环境字段始终 `unverified`。输入或工具失败时返回非零，生成 ELF 不等于可加载、可关联或能识别进程。

将 `--profile capture` 改为 `--profile lifecycle` 可编译新增的生命周期观测对象；默认仍为 capture。lifecycle 固定快照两份 C 源码，要求同时存在 connect4/sockops sections，记录两个程序各自的 socket 观察。它不构建 tuple 索引、不删除记录，passive 的 namespace/PID 保持未证明；TCP_CLOSE 不能当作 accepted 证据可回收。精确 ABI、v5.10 helper 缺口、v6.6 备选基线和未来验收顺序见[生命周期契约](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md)及[新准备记录](../../docs/validation/connector-lifecycle-preparation.md)。

准备器的假工具测试只验证离线边界：

```sh
python3 -m unittest discover -s experiments/sourceidentity -p 'test_prepare.py' -v
```

有限loader的假接口验证见下文；获得隔离环境后仍须实测字段/配置/map、freeze、attach 连续性、故障注入及清理。不提供默认提权、pin 或系统安装脚本。上游 v5.10 的 task/kernel 字段读取还受能力检查约束，仅有 BTF/clang 不表示具备权限。相关[固定源码](https://github.com/torvalds/linux/blob/v5.10/kernel/bpf/helpers.c#L662-L701)只作设计依据，不作为厂商内核实测。

## 已有对象的只读检查

无需libbpf开发头文件；检查器通过ctypes调用明确指定运行库的open/read/close接口，不调用load/attach/pin。以下是本次已固定的对象和库；换环境时必须使用当地经过登记的路径与摘要：

```sh
python3 -B experiments/sourceidentity/inspect_object.py \
  --profile lifecycle \
  --object /data/1/weixiaoxian.wxx/dev_tool/Hoopa/private/t132-lifecycle-preparation/hoopa-sourceidentity-lifecycle-52iz6olv/lifecycle_snapshot.bpf.o \
  --object-sha256 92c5052169b195977f715c893da787017db8dca681b7d50ba13d262a7d20386e \
  --libbpf /usr/lib64/libbpf.so.0.6.0 \
  --libbpf-sha256 dcb0380081532521e388ee9e3e6a6ee13340897e8327fd935b2a705c9b8df1ef
```

`status=inspected`只确认程序名称/section及map声明符合profile，不表示内核可接受对象或身份已关联。本机发行包版本不能直接等同上游同名tag，实际走旧迭代器；调用参数、关闭路径和输入拒绝有8项假库测试。单条记录解码另有10项纯bytes测试，uint64以十进制字符串保真，名称与关联不提升为verified。源码/API和范围见[契约第7节](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md#7-离线对象检查与单条记录解码)。

## 有限 loader：接口与准备阶段验证

`run_observer.py`默认消费lifecycle对象，也可显式选`--profile gated`（见下节），默认拒绝加载；`--help`可查看参数。不带`--allow-bpf-load`时，在读取输入及构造native backend前返回exit2。该开关表示实验命令的显式意图，不代替隔离环境及加载授权。准备阶段未执行加载；后续仅在受控 guest 中执行，见页首验收。

未来运行须提供对象与libbpf的绝对路径/SHA256、预登记的非零generation/netns-cookie、入口端口、已有输出父目录，以及预先准备的空cgroup v2 domain叶目录。命令不创建cgroup、不搬移进程、不启动客户端，不修改系统代理。先核验对象、共享map、配置写入/readback/freeze与冻结后写入拒绝，再通过直接BPF_LINK_CREATE挂接两个程序；不回退为旧式attach。全部核验后才写出`attached_observer`，实验harness再按预登记安排客户端/服务端。空目录检查不锁住外部变更，也不证明隔离、初始PID namespace或完整监测范围。

观察窗口必须大于0且不超过60秒；这是观察期上限，不是加载等内核调用的总超时保证。退出按逆序各关闭一次持有的link，然后作有界诊断扫描并关闭对象。部分成功失败、输出失败和中断保留原错误及清理错误；异常时不伪造空快照。信号先记停止意图，在FD登记后的安全边界处理，避免中断落在FD创建与登记之间。

**关闭link不证明在途回调结束；MAP_FREEZE也不能阻止BPF程序写观察map。** 因此扫描始终标注quiescence/completeness/association为unverified，不能用零fault或空map推导完整性。成功流程exit0和`observed_unverified`仅表示观察流程完成。出现非零fault、解码失败、读取或清理失败则exit1；原始key/value hex保留，未知不是零。输出目录0700、JSON文件0600，没有生产账本或持久恢复保证。

本轮验证命令只使用fake CDLL/backend与纯bytes，不加载BPF：

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_native_loader.py' -v
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_loader_session.py' -v
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_run_observer.py' -v
```

停止语义、API来源与尚缺的真实门槛见[契约第8节](../../specs/001-hoopa-proxy-manager/contracts/connector-lifecycle-spike-v1.md#8-有限-loader-准备与停止边界)。该工具不关联active/passive，不生成passed CapabilityRecord，不接入路由。

## 原 accepted socket 见证

[acceptedwitness/](acceptedwitness/) 是独立Go实验包：包自身AcceptTCP，向调用者返回前在同一RawConn.Control内读取socket类型/端点与SO_COOKIE/SO_NETNS_COOKIE；私有Conn绑定快照，不接受任意FD或独立witness来冒充原入口。失败关闭新连接，保留捕获/关闭错误；Close后的快照只是历史。

定向fake race、vet和gofmt已验证。本机loopback的普通Close窗口通过，但NETNS option 71返回ENOPROTOOPT，完整见证正例skip；不能把package exit0称完整通过。真实测试由HOOPA_WITNESS_LOOPBACK=1显式启用，仅使用临时127.0.0.1端口，不加载BPF。其他平台stub仅离线编译。实际命令、4顶层/38含子测试计数与环境限制见[验收](../../docs/validation/accepted-socket-witness.md)，接口边界见[契约](../../specs/001-hoopa-proxy-manager/contracts/accepted-socket-witness-v1.md)。

## 有限 accepted 关联研究模型

固定核心后续的[同名父子/PID复用实测](../../docs/validation/connector-core-lifetimes.md)新增2VM/4流/26帧，两类场景均按原实例核对一致；机器记录（本地原件 `core-lifetimes-validation.json`，不随Git上传）与其他历史批次分列，不升级正式能力。

`association_model.py` 枚举有限合成流的 connect/active/passive/原 FD witness 查询交错。只插入、不回收的 tuple 索引在完整覆盖等前提下可作条件关联研究；模型同时复现忽略 fault、漏采 active、取错另一真实 FD 三类错配。后两类能零 fault 错绑，不能由计数正常推导身份正确。C 不预知客户端临时端口，Q 不消费 oracle 的正确 cookie。

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_association_model.py' -v
python3 -B experiments/sourceidentity/association_model.py
```

这是纯模型，14 项测试通过；没有新增 BPF profile/ABI、在线 resolver 或核心接线。真实范围/覆盖、原 FD 见证及停止稳定仍须独立证明，runtime/verifier/identity_capability 固定 unverified。边界及后续顺序见[契约](../../specs/001-hoopa-proxy-manager/contracts/connector-association-model-v1.md)和[验收](../../docs/validation/connector-association-model.md)。

## gated 停止写入候选

此profile增加capture_gate和不attach的hoopa_stop程序；原connect/lifecycle body均由admit→body→finish包围。停止控制程序在同一锁下置closed，不覆写active；BPF_F_LOCK读取closed且active=0后才能扫描。正常、失败和中断清理都先尝试stop，再逆序关闭link。固定源码的同锁release/acquire契约支持此顺序；准备阶段没有真实运行证据，后续两流运行观察到 closed、active=0、admitted=completed=35、poison=0，但真实停止交错矩阵仍未完成。

离线准备命令沿用上文参数，只将`--profile`改成`gated`。该profile会提前快照三份源码，要求connect4/sockops/tc三个有效section。对象检查器支持同名profile；capture/lifecycle默认兼容。三个对象均有本轮实际编译/解析记录，旧对象和旧JSON未覆盖。

显式运行gated或namespaced时，loader还会通过BPF_PROG_TEST_RUN执行一次停止控制程序。这是内核执行，不能把名字里的test当作离线操作。准备阶段未执行，后续受控 guest 已实际调用。默认2秒/1001次是控制调用返回后的轮询界限，不是同步内核调用的硬超时。

新增纯模型与fake接口测试：

```sh
python3 -B experiments/sourceidentity/stop_model.py
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_gated_loader.py' -v
```

writer_stop只保存候选gate状态；完整结果仍observed_unverified，quiescence/completeness/association保持unverified。停止之后未准入的close回调可被排除；停止写入不证明TCP结束、整个采集完整或身份关联成立。40B ABI、两种错误方法的反例和真实验收顺序见[停止契约](../../specs/001-hoopa-proxy-manager/contracts/connector-stop-spike-v1.md)。

## 历史 proc holder 探针（保留旧口径）

仅用于 Linux amd64、同有效 UID、同 network namespace 的 IPv4 loopback ESTABLISHED TCP。读取 `/proc/self/net/tcp` 的原始客户端 tuple/inode，再核对持有 FD 的进程 PID、`stat` comm 名称、start ticks、boot ID 和 netns。它不转发流量、不修改代理、不调用特权采集。

调用位置必须在原应用的首个入口、原 socket 仍存活且转发尚未开始。下游看到 GOST 的 socket 时再调用，得到的仍是 GOST；不能倒推原应用。GOST admission 的同步来源地址是可接线候选，但本探针尚未与它接线，也没有目标域名、终末计数或实际线路证据。

发现多个可见持有进程返回 ErrShared；无法完整读取同 UID owner 集合则返回 ErrUnknown + ErrVisibility，不跳过权限错误并声称唯一。进程/FD/命名空间或 socket 前后变化均拒绝。进程名取自 comm，可能截断，不是可执行路径。扫描限 4096 个同 UID 进程。

跨 UID FD 共享、FD 传递竞态、不可见进程和非原子 `/proc` 扫描均未解决；当前包不能提供通用生产身份保证。不能将这一探针视为 G01/G05 通过，也不能默认提升权限。

测试会启动两个同名 helper，各发起 50 个连接，在入口完成识别前不发送客户端 payload；另测父子共享 FD 拒绝。这仅验证入口识别，不是 100 条代理短流的完整验收。在本次宿主上，100 连接断言因同 UID 进程 FD 目录不可读而未验证，未作为通过记录；原失败观察保留在任务记录。出现该条件时测试明确 skip/unverified，不能计入成功数。

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -v ./experiments/sourceidentity
```

需允许回环 socket。生产方案以[事件源重规划](../../docs/adr/005-telemetry-replan.md)的能力契约及后续真实探针为准。

## 三进程实验驱动与完整材料检查

新增 `run_fixture.py`、`fixture_processes.py` 与 `check_fixture_run.py`。外部lab在observer挂接后，将一个驱动进程放入既有cgroup，驱动再启动一个服务端和两个客户端；不会自行加载BPF或搬移进程。默认缺少显式开关时拒绝执行。成功材料保存为独立11文件目录，完整核验退出码、EOF、PID/argv、时间顺序、摘要以及scope声明对observer结果的绑定。

详见[执行契约](../../specs/001-hoopa-proxy-manager/contracts/fixture-run-v1.md)与[准备阶段验收](../../docs/validation/fixture-run.md)。该阶段58项定向测试通过；当时真实子进程仅无网络Python程序，驱动/作用域使用假接口。后续真实运行与最新测试见页首。输出 `consistent_unverified` 仅说明材料一致；此 driver 仍是诊断工具，不能用于生产路由，T132未勾选，139/21不变。

固定核心另完成[同名父子并发/PID复用](../../docs/validation/connector-core-lifetimes.md)、[原connector退出后的FD使用](../../docs/validation/connector-core-earlyexit.md)和[PID/net namespace范围拒绝](../../docs/validation/connector-core-namespaces.md)。各自有限样本与失败分开记录，不提升名称一致性或正式身份能力。

[名称原子指令可行性](../../docs/validation/connector-name-feasibility.md)完成一个独立VM探针；该对象不接入当前核心，也没有读取/认证实际连接进程名。记录有限模型及ftrace漏钩子条件，避免把可加载和零miss误作完整名称保证。

[固定核心四元组复用](../../docs/validation/connector-core-tuple-reuse.md)已验证同一世代内第二次实际连接在协议前拒绝，首条历史保持；保留首次夹具gauge误判失败。insert-only表的长期回收/可用性仍未实现，不能将负例通过当作生产可用。

[exe_file快照最小验证](../../docs/validation/connector-executable-snapshot.md)实际完成内核引用/路径kfunc和同cookie准入四阶段；这是独立路径字段的可行性，不是comm认证或核心已接线。初版checker失败、修订后同材料通过及后续路径边界均明确分列。

[可执行文件路径边界](../../docs/validation/connector-executable-path-boundaries.md)另验证改名、超长拒绝、恢复和删除原文，历史记录保持；每次使用新socket，同cookie重试与消费语义另验。
