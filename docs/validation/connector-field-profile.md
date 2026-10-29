# Source v5 受限字段能力的验收记录

2026-10-25。按[冻结契约](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v5.md)，新0015候选完成一个独立正向VM及三个实际受保护启动拒绝用例。正向VM、canonical提取、严格checker均exit0；1VM、2条正常流、13帧、38个导出和1个归档的原始长度/SHA独立复核。详细制品和可复算元数据见验证JSON（本地原件 `../../experiments/sourceidentity/field-profile-validation.json`，不随Git上传）。

## 新候选与逐字段含义

[0015候选manifest](../../experiments/mihomo-events/core-field-profile-candidate-manifest.json)固定上游v1.19.26 commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`。新核心51320639B，SHA `1ef1b53ae41435596844c264ae5e1e5201ce95b5d2762be872371023f4d6af25`；干净001–015源码树 `8f79c90508d8a3719eee356778fc513f872c615b0fba2591c0191ca4ecc26020`。默认打印1.10.0不是源码版本。组合BPF仍为 `df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49`；新镜像42032013B、SHA `e4cc16ef8bce6f281c2b940632e5672a631c5acb8e2050dbc53961e697ef0e2a`。

本次显式选择 `linux_amd64_loopback_connect_v1`：handoff-v4、source5、connector-v3、executable-call-v2及manifest2相互绑定。instance、program_name、program_path分别按固定来源声明available；comm仍unverified。available是可信固定候选在登记边界内的逐连接字段承诺，不是parser对任意外来帧的真实性认证，也不回溯升级旧source1–4。

核心76实际持有21个唯一移交FD并恢复CLOEXEC。实际只读map lookup保存的24B scope为 `c2dfd63262d67021fdffffeff8ffffef0100000000000000`，解析为capture generation `2409661517891297218`、user inode4026531837、mount inode4026531832、schema1/reserved0；两条opened内field_profile均与该原文一致。legacy process精确为unknown/legacy_process_fields_unused，两个旧connector capability/path键已移除。

## 实际转发与来源

运行 `vm-positive-xuab9n1d`：

| 入口 | 原PID/TID / start ticks | client / accepted cookie | 实际上游 | attempt ready / flow duration | 上传/下载 |
| --- | --- | --- | --- | --- | --- |
| HTTP CONNECT / alpha.test | 86/86 / 479 | 4097 / 4098 | node:a，Http | 11ms / 12ms | 33B / 33B |
| SOCKS5 / beta.test | 91/91 / 525 | 2 / 3 | node:b，Http | 31ms / 32ms | 33B / 33B |

两入口协议不同，上游节点均为Http；两条quality的protocol均为http_connect、ready_kind均为http_connect_accepted、definition均为forward_setup_v2。31ms不是SOCKS5上游ready样本，也不是Admit耗时或性能门槛。

两条296B原exe记录的call token为1/3、consumed1，原cookie、PID/TID、leader start与BOOT区间均对应各自独立socket/proc oracle；路径 `/bin/python3`、basename `python3`、dev2/inode8。basename来源与kernel comm分开。每流发送33B后SHUT_WR，再接收精确33B及EOF；独立node/target payload绑定和实际线路均核对。来源完整epoch、两closed、无gap；只读恢复两尾均0，派生报告不补历史身份。

客户端先经自有wrapper完整降权，再以同PID/start exec：全部UID/GID65534、Groups空、五组capabilities为0、NNP1；PID1独立核验并release后才创建目标socket。两客户端均实际观察到root/leaf cgroup.procs仅写打开EACCES、pidfd_open成功但请求核心原FD220的pidfd_getfd为EPERM、BPF command30/4B link ID请求EPERM；没有write/detach/update，只关闭自有FD。最终Owner admitted2/rejected0/revoked=false、live gauge0，四项诊断均observed0，核心SIGTERM0。旧status三个identity布尔仍false；它们不代替source5独立字段声明，也不代表旧PROCESS规则获得能力。

## 实际preflight与定向重开

同一新候选通过显式path/SHA的TestIdentityPreflightActualCandidate：1顶层、4含子项pass、0skip。required comm、重复instance及未知字段三种输入均得到真实exec.ExitError/exit1与固定 `Hoopa protected configuration rejected` 标志；泛启动、路径或动态链接失败不能计作通过。该实际结果说明核心进入受保护拒绝，具体字段分支和先于PrepareGuard/Reserve/Listen的次序另由纯validator、真实runHoopaGuard入口测试及源码调用序支撑，不能仅凭最终无端口就断言从未短暂监听。

v5缺终末只读pending/null、原帧不改及重复重开稳定，来自独立定向Go测试；既有独占writer完整扫描有结束证据时才派生gap/null。本次真实VM只验证正常完整终末，没有新增crash VM，也没有把internal recovery report当成VM公开API。

## 准备与范围

本夹具49项纯/fake检查、协议/Owner/config/writer定向测试、干净构建和decoder构建均分别保留，不累计为本次真实flow样本。准备阶段遗漏PYTHONPATH、checker沿用旧诊断handoff-v3、fixture/测试路径及新测试预期错误的记录与修正均保留；冻结前已修正。原PREPARATION.md的“尚无镜像”文字也保留，最终review已独立确认实际镜像及pins一致。本批实际VM无失败。

本结果沿用[namespace守卫的有限边界](connector-namespace-guard.md)：固定6.18.54.1/amd64、普通非TFO IPv4 loopback、受控初始namespace/root-leaf和先挂接后建socket；loader/core及管理员可信。低权限客户端结果不是生产launcher或任意特权干扰防护证明；不主张原子comm、其他内核/平台、长期容量或本轮新增性能量测。程序名可用仍不开放任何旧PROCESS/UID规则。正式G01新增0、旧0/100，P0/P1未放行；T132/T137由主代理另行裁决，本记录不自行勾选任务。
