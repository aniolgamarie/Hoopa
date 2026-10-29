# T132 隔离 Linux 实测与加载器修正

日期：2026-10-24。状态：已取得第一组真实两客户端校准，**T132 整体未完成**。本记录接续[驱动准备验收](fixture-run.md)；机器摘要为 isolated-lab-validation.json（本地原件 `../../experiments/sourceidentity/isolated-lab-validation.json`，不随Git上传）。139 项任务仍完成 21 项，正式 G01 的旧 0/100 不重评、不增加分母，T137、P0/P1 不放行。

## 授权、来源与环境

用户后续明确授权继续准备和执行隔离实验，覆盖此前“暂时没有环境，先完成准备”的阶段范围。宿主实际提供可用 `/dev/kvm`：API version 12、USER_MEMORY 扩展与创建/关闭空 VM 均实测成功。宿主仍为原 5.10 内核与 cgroup v1；没有升级宿主内核、安装系统包或修改其 cgroup、代理、自启动。

此前 Alpine/AlmaLinux/Rocky 的地址核对与下载分开记录：观察到 TLS 握手失败或直连超时，没有收到能证明地址不存在的 HTTP 404。改用官方 GitHub release 资产完成准备，不关闭 TLS 校验。

| 输入 | 实际固定值与核对 |
| --- | --- |
| 私有 QEMU | [Kata 3.12.0](https://github.com/kata-containers/kata-containers/releases/tag/3.12.0) 官方归档中的 QEMU 9.1.2；归档 405342596 B，xz/tar 检查通过，SHA256 `75f4a22da355f8ab6394700c7cc145c7ab81d95286f846890020f2eab420c97d`；未取得独立官方归档签名/摘要验证 |
| 来宾内核 | [WSL 3.0.3](https://github.com/microsoft/WSL/releases/tag/3.0.3) x64 MSI 的 `cab3.cab/kernel`，实际启动为 `6.18.54.1-microsoft-standard-WSL2`；MSI 367575040 B，SHA256 `ae977286974255bd26bf925f00f40f5692c6b2084155cbc0563c09aea091f211` 与官方发布值一致 |
| 解包工具 | 官方 [7-Zip 26.05](https://github.com/ip7z/7zip/releases/tag/26.05) Linux x64；仅运行归档工具，没有运行 Windows 安装器或包内 Windows 程序 |
| libbpf | 私有构建 1.6.2，共享库 SHA256 `1c928f61158f25d39cd3fbc7ddafeebec2448886225e2c1824455fcfcd15d04c`；旧 0.6 与宿主库保留 |
| BPF 对象 | 原 namespaced 对象，SHA256 `ffb7aa7ff77f55160a7da16a98a4056ff4ff5a6302cdbacb19601ad4cdc4735a`，没有改写对象或其历史 manifest |
| 真实 fixture | 原 Go fixture，SHA256 `b3ffcf47e1f2b4373e35c9d430178ab5b65b8ff28e4e3deb38bf786fbd944317` |

6.6 仍是设计中的首选验证基线，本次使用实际取得的 6.18 候选推进实验；不据此设定产品最低内核或宣布正式平台支持。WSL 的 [exact-tag 配置](https://raw.githubusercontent.com/microsoft/WSL2-Linux-Kernel/linux-msft-wsl-6.18.54.1/arch/x86/configs/config-wsl) 启用了 BTF、BPF syscall/JIT、cgroup BPF 与 time namespace；该配置头部版本与发布说明不完全一致，因此以实际来宾版本、BTF 摘要和运行行为为准。曾根据 GitHub 渲染页 `find` 无匹配误判配置缺少 BTF，已用完整 raw 文件纠正，未据此放行。

QEMU 使用 KVM、2 vCPU、1 GiB 内存，无网卡、磁盘、host share 或控制 socket，直接启动私有 initramfs。仅来宾 PID 1 挂载 proc/sysfs/devtmpfs/cgroup2、启用来宾 loopback，并创建空 domain leaf；观察器挂接后，launcher 在 fresh child 的 exec 前将其放入该 leaf，再启动 fixture。所有 mount、BPF、cgroup 和 TCP 操作均在来宾中。每次 QEMU Popen 由启动器等待并回收，150 秒外层期限只管理该自有 VM；不声称硬中断任意内核 syscall。

## 实测失败与修正

全部记录在忽略的 `private/t132-kvm-lab/`，没有覆盖旧运行。

| 私有运行目录 | 结果与处理 |
| --- | --- |
| `vm-readiness-suwfek6n` | Kata 6.7 来宾启动/关机成功，但缺少 time namespace；readiness 拒绝。该候选配置也未提供所需 BTF，未运行 BPF |
| `vm-calibration-ro5hy5ne` | WSL 来宾启动；私有脚本错误要求 cgroup 根目录提供 `cgroup.type`，被拒绝。改为 mountinfo 核对 cgroup2，仅在新 leaf 检查 domain/空成员 |
| `vm-calibration-cf427uw5` | BTF、namespace、cgroup2、双 cookie 前提实测成立；libbpf 0.6 报无法加载目标 BTF，未挂接、未启动客户端 |
| `vm-calibration-kxojywqx` | 新库成功加载原对象，随后 map 元数据校验拒绝；原笼统错误保留 |
| `vm-calibration-uhkf1l3s` | 增加有界元数据诊断后，确认 `connect_records` 声明 flags=8、内核 map info flags=0；没有降低权限要求 |
| `vm-calibration-64kp6nzk` | 修正 map/FD 权限校验后，真实加载、挂接、两客户端、停止与完整材料核对完成，结果为 `consistent_unverified` |

两项正式源码修正：

- 停止程序改用 `bpf_prog_test_run_opts`，旧/新库均有该接口。根据固定 1.6.2 头文件实际 C probe 核对 LP64 80 B 结构及全部偏移；零初始化、64 B dummy input、repeat=1、NULL packet/context output，保留返回值、错误、排空与清理顺序。旧接口在 1.x 已移除。
- 按 exact-tag [kernel/bpf/syscall.c](https://raw.githubusercontent.com/microsoft/WSL2-Linux-Kernel/linux-msft-wsl-6.18.54.1/kernel/bpf/syscall.c) 的 `bpf_map_flags_retain_permanent`/`bpf_get_file_flag` 与 [fs/fcntl.c](https://raw.githubusercontent.com/microsoft/WSL2-Linux-Kernel/linux-msft-wsl-6.18.54.1/fs/fcntl.c)，从 map object flags 期望值中分离 `BPF_F_RDONLY/WRONLY`，再用 `F_GETFL & O_ACCMODE` 严格验证实际 FD。其他元数据仍精确比较，权限缺失或 FD 无效仍拒绝，没有试写 map。结果新增 `kernel_objects.map_access`，保留原 maps/programs 字段。

跨 VM 原样材料检查另外增加显式 `--original-run-directory`，仅用于原 argv 的路径比较；宿主仍通过当前目录 FD 读取固定普通文件。原目录声明不认证来源，原 receipt 与摘要不可重写，详见[驱动契约](../../specs/001-hoopa-proxy-manager/contracts/fixture-run-v1.md)。

## 首组真实校准的证据

成功来宾 BTF 为 6018902 B，SHA256 `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9`。boot ID 为 `ac7e3b7e-7320-4bc9-a321-31f2b201fc21`，目标 cgroup ID 22，socket netns cookie 3。

| 客户端 PID | 客户端 socket cookie | 原 accepted cookie | 内核观察的启动 boottime ns |
| --- | --- | --- | --- |
| 83 | 4097 | 4098 | 3754337680 |
| 84 | 2 | 3 | 3757824535 |

预登记 token 由真实连接传输，原 FD witness 与停止后的 raw map 独立候选核对一致。两个客户端 `/proc` start ticks 均为 375；本次只保留这些事实，**尚未独立完成启动时钟换算/名称/路径验收**。不能把两个数值存在等同于完整启动身份已证明。

本轮得到 2 条 connect、4 条 established、4 条 TCP_CLOSE 观察，fault=0；停止报告 closed=true、active=0、admitted=completed=35、poison=0，两条 link 均关闭。observer、driver 和三个 fixture 子进程均 exit 0，空 leaf 与最终 TIME_WAIT 分别记录。35 是观察回调次数，不是应用连接数；TCP_CLOSE 也不表示进程退出或立即可安全回收关联键。

完整检查使用原样导出的 22 个普通材料文件；被导出器省略的 fixture 可执行字节从原 pinned 文件恢复并与原 receipt 核对。host review 单独生成范围声明，绑定 registration/input/marker/run/result 五份原始摘要。声明具体依据受控启动源码、初始 namespace、空 leaf/无预存 TCP、exec 前放置、挂接 marker 与 owned FD 生命周期；连续性属于该封闭实验路径的操作者判断，未伪装成独立持续内核审计。报告仍将 scope/identity/startup/completeness 等字段标为 unverified。

## 已追加的三个真实故障运行

先保存预登记与脚本摘要，再启动独立来宾；原成功两流材料和正式分母不变。完整私有登记/汇总为 `runtime-negative-registration.json`、`runtime-kill-registration.json`、`runtime-negative-summary.json`。

| 场景与目录后缀 | 实际结果 | 判定 |
| --- | --- | --- |
| wrong-driver-scope / `bh_ookb1` | driver 1、observer 0；driver 报 placement/members changed，未创建 fixture 收据或子进程；目标 leaf 始终空，连接 maps 空 | 命中原预登记；真实准入拒绝，仅证明这一错误范围场景 |
| observer-terminated / `1r5ztgz3` | 挂接后 SIGTERM 自有 observer；observer 写 interrupted result 后 exit 1，driver 见已停止结果后 exit 1；零 fixture | **原预登记失配**：主代理误把 SIGTERM 当默认信号退出，原预期 driver 0 / observer -15 / 无 result 不改判。实际观察是安全拒绝已停止观察器，不能算原定缺结果场景通过 |
| observer-killed / `cliyp3q8` | 新登记后使用 SIGKILL；observer -9，无 result；driver 0，三个 fixture 均 exit 0、两流实际完成；整轮 calibration failed | 命中新登记的有限拒绝判据；只证明结果不被误接纳，**没有证明流量发出前的保护** |

SIGKILL 反例把下一步收敛到核心转发前的来源有效性/原 socket 身份判断：当前 driver 依赖 attached marker 与已存在的停止结果，来不及写 result 的失联仍可能留下看似有效的 marker。该工具是实验编排器，不能被直接当成生产准入或进程选路保护。单独检查进程存活也不能解决检查后退出的竞态；必须在在线解析/核心接线设计中冻结实际失联拒绝边界，并实测。上述三种实际观察不能替代中途 detach、在途回调停止、FD 转交、tuple/PID 复用或完整身份矩阵。

## 分层验收与下一道门槛

| 验证层 | 结果 | 边界 |
| --- | --- | --- |
| loader/stop/session 定向测试 | 最终 58 项通过 | fake CDLL/字节/控制逻辑；含 opts ABI、错误返回、FD 权限与无效 FD；前置失败日志保留 |
| 完整材料 checker | 20 项通过 | 合成材料与路径/摘要拒绝测试，不代替真实运行 |
| 实际旧/新库离线构造/open/close | 两库均成功 | 没有加载宿主 BPF |
| 私有 KVM 来宾两客户端 | 原始 bundle 与完整 run checker 均一致 | 有真实 socket/BPF/load/attach/stop；仅此内核、对象、库和有限两流 |
| 从真实材料派生的 5 个离线反例 | 交换原 witness、错误 PID、错误 namespace、缺客户端、缺 stop 均拒绝 | 人工变异副本，不是真实 kernel/故障注入 |
| 对真实导出目录执行的 2 个 CLI 反例 | 默认迁移路径及错误原目录均拒绝 | 验证路径声明边界，不改变原材料 |

下一步优先设计并验证核心转发前的身份来源失联拒绝；并在这个已可用环境继续真实停止交错、中途 detach、范围外连接、复用和 FD 转交等负例，核验原进程启动身份与名称，再冻结在线关联并接入核心。单次 active=0 或零 fault 不证明所有未来交错、范围或唯一归属；离线拒绝也不能保护已发生的在线路由。T132、T137 与正式 G01–G03 仍未通过，基础管理以后的阶段不提前交付。

执行分工为主代理决策和真实 VM 验收、executor（工具配置 gpt-6.1-sol/medium）负责私有依赖构建、checker 和独立复核，scout（gpt-6-luna/high）核对官方源；另有只读 explorer 映射 ABI/测试调用链。最多两个子任务并行，单文件单写入者。所有二进制、镜像、下载分块及原始运行日志仍位于 `private/`，不进入 Git。
