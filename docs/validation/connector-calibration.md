# T132 小规模校准准备验收

历史准备阶段记录。后续授权后的真实运行与修正见[隔离 Linux 实测](isolated-linux-lab.md)；本文原测试范围、旧 JSON 和未实测结论保留其当时范围。

日期：2026-10-24。延续用户“暂时没有，先完成原型准备”的授权，本轮将namespace观察、原FD见证、受控流量与独立核对串到同一[执行契约](../../specs/001-hoopa-proxy-manager/contracts/connector-calibration-v1.md)。**完成的是可执行工具及离线验证，尚未完成真实内核校准或T132。** 机器摘要见calibration-validation.json（本地原件 `../../experiments/sourceidentity/calibration-validation.json`，不随Git上传）。

## 实现与判定边界

1. `namespaced`复用gated的三个程序、六个map及停止路径，仅将established值扩为56B/schema2。offset48是从实际socket读取的namespace cookie，active/passive都要求非零且与配置相等；close仍40B/schema1，不能推断其namespace。准备器先快照四份C源码，解码器显式按profile拒绝混用。
2. `fixture`是独立Go实验命令。服务端通过acceptedwitness包自己Accept；客户端从原TCPConn读取socket cookie/netns和端点，另读取PID、proc start ticks和pidns inode。两个进程通过实际连接交换预登记token，双向帧和EOF均检查。serve最多缓冲16条结果，全部连接及listener关闭成功后才输出connection；失败保留stderr/非零退出。该命令不启动其他进程、不改变cgroup、不加载BPF。
3. `calibration.py`有界只读JSON/JSONL，再从raw map bytes解码，忽略缓存的observation字段。候选只根据generation、实际netns、端点和socket cookie生成，独立token只在之后核对。缺active、错原FD见证、重复tuple/key、fault、namespace/PID矛盾、缺listening及重复JSON键都会拒绝。正例只返回consistent_unverified，不产生passed能力。

固定Linux v6.6的sockops与sock_addr helper均读取socket所属namespace，可共用既有helper声明；这是设计依据，见[官方filter.c](https://raw.githubusercontent.com/torvalds/linux/v6.6/net/core/filter.c)。本机5.10厂商内核此前已实际返回SO_NETNS_COOKIE ENOPROTOOPT；本轮没有再次运行这个已知失败场景，更没有升级宿主或将v6.6源码当作本机支持证明。

## 实际验证

| 检查 | 实际结果 | 不能据此推出 |
| --- | --- | --- |
| 共享Python实现回归 | 129项通过；fake libbpf/backend与纯bytes，0失败/跳过 | 真实内核load、freeze、attach、gate正确性 |
| 独立checker定向测试 | 最终12项通过；uint64精度、raw bytes、漏采/错配/重复/顺序/输入拒绝 | 覆盖完整、可信来源、启动实例唯一 |
| checker CLI | 合成一致输入exit0；交换真实形状witness、缺listening、重复JSON键各exit1 | 实际流量成功或新身份分母 |
| fixture | fake race、vet、gofmt、build通过；未带allow命令实际exit1且stdout为空 | 本机支持双cookie或已运行两个真实client |
| 三个受影响BPF profile | namespaced、gated、lifecycle各4步真实离线准备成功，实际libbpf只读解析成功 | verifier或helper运行支持 |
| 旧profile兼容 | lifecycle的connect4/sockops、gated的connect4/sockops/tc指令段分别与旧对象逐字相同 | 新namespaced运行正确或全平台支持 |

共享回归之后只新增独立checker和fixture；它们定向验收，没有为了增加次数重复全量测试。实际命令/退出状态与固定输入摘要保存在忽略目录`private/t132-complete-preparation/`，包括`python-regression-final.log`、`calibration-test-final.log`、`fixture-*-final.log`、`offline-build-inspection.json`及`synthetic-calibration/checks.json`。合成CLI各案例使用独立fixture JSONL，共享同一registration/input/result；每次输出记录并核对实际四个输入的路径和摘要。编译产物留在private，git仓库保存源码、契约与摘要。

初次checker tests-first因模块未实现而失败，日志保留。稳定审查发现listening事件可缺失，已修为必须恰有一次且先于serve记录，并补缺失/重复/晚到测试。初版11项与最终12项日志分开；这些是开发过程记录，不是内核身份失败分母。没有回写旧验证JSON或001–007核心补丁，原G01 0/100及首次active失败保留。

## 尚缺的真实证据与实施边界

校准器只能判断输入之间是否一致。它不认证JSON来源、不证明预登记先于流量、不核实外部launcher的实际cgroup放置或连续attach；这些必须由隔离lab独立留证。proc start ticks与BPF start boottime ns仅分列，没有做时钟单位换算，名称/路径也未认证。writer停止不等于采集完整，stdout或内核调用也没有硬总超时保证。

下一阶段需在获授权的隔离Linux中，先验证两个独立客户端的小样本、真实原socket双cookie和scope/stop；失败时保留错误并定位helper、挂接、字段或顺序问题。然后才有依据审查在线关联、名称/启动身份与核心包装接线。不得将离线最终拒绝作为已执行路由的安全保证；不得跳过小样本直接重跑正式100流。

本轮未执行socket流量、BPF load/attach/TEST_RUN、cgroup变更、依赖下载或系统配置修改。requirements只读16/16；139任务/21完成，T132/T137/T136及正式G01–G03/P0/P1未放行。没有新增平台或性能支持声明。

分工实际为executor（gpt-6.1-sol / medium）独占Python profile接线、fixture及新契约，scout（gpt-6-luna / high）只读核查固定API和稳定实现；主代理负责C扩展、候选/核对边界、离线实测与最终验收。最多两个子任务，无再派生；共享文件按阶段转交，没有并行写入。
