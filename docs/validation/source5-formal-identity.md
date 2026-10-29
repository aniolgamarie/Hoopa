# Source5 原始进程归属：受限正式验收

2026-10-26，固定0017候选的 **T127受限正式验收完成**：正向100/100、PID复用2/2、缺凭据2/2、BPF管理权限1/1、三值规则14/14分别通过；分母保持独立。总验收入口重新核对708份文件，真实Go race入口1通过、0失败、0跳过。[验收决定](../../experiments/mihomo-events/source5-identity-acceptance.json)与执行记录（本地原件 `../../experiments/mihomo-events/source5-identity-gate-validation.json`，不随Git上传）独立保存，历史观察的task=false标记保持原义。T012使用同批证据完成G01受限评审，项目140项/27完成；T128恢复、T129质量与完整规则矩阵继续，P0/P1未放行。

本次依据[正式oracle契约](../../specs/001-hoopa-proxy-manager/contracts/source5-formal-oracle-v1.md)，先完成同冻结镜像10条准备，再一次性登记100条。ordinary、descendant、fork-writer、scm-writer、thread各20；每类两个域名和HTTP/SOCKS5四种组合各5条。独立父进程在connect前读取原始PID/TGID/TID、启动身份、执行文件和socket cookie，完成truth登记及fsync/release屏障，随后严格核对原始来源事件。FD转交或继承后的writer不会改写原始connector。

| 观察 | 结果 |
| --- | --- |
| 固定正向槽位 | 100登记，100通过，0失败/未执行 |
| 独立VM / 完整来源epoch | 5 / 5 |
| 原始帧 / 导出文件 | 612 / 210 |
| 原始进程实例 / 域名 | 100 / 2 |
| 实际线路 | node:a、node:b各50条 |
| 应用字节及终末 | 每条上传33、下载33；唯一token关联独立节点和目标记录 |
| 原始执行文件 | 全部/bin/python3；支持本批同名实例验证，不表示程序种类覆盖 |

完整机器观察（本地原件 `../../experiments/mihomo-events/source5-identity-positive-validation.json`，不随Git上传） SHA256为`639b8bf02fbc72b0ed9241105a88ca581bc7826cd172ec1ac491af0c2ffc47e4`，固定核心SHA256为`5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`。聚合器只产出观察，原文accepted_new_samples=0不承担验收；主代理据冻结观察、独立复核和真实入口接受本批100条正向结果。本段记录正向批次当时状态；后续独立边界和总验收见下文。

首轮检查器把合法bytes_checkpoint误写为checkpoint，原60/100失败记录（本地原件 `../../experiments/mihomo-events/source5-identity-first-formal-validation.json`，不随Git上传）保留；更早的0/100也保留。本次使用独立lab、新准备、新登记，不重新判定旧失败。仅两个事件种类比较修正，核心、来源协议、guest镜像不变。

新增[公共只读入口](../../experiments/mihomo-events/source5_identity_probe.py)与TestSource5RegisteredIdentityPositives：传入登记路径/SHA及固定validator路径/SHA后，重新核对已有原始证据；无配置时明确skip为未验证，配置不全则失败。真实四变量定向race验证1通过、0失败、0跳过；Python纯检查11通过、vet通过。入口依赖本地private证据及固定验证器，属于实验验收入口，尚非可脱离证据树分发的产品功能。它不会启动VM或核心。

本批7个周期检查点均0/0，证明合法周期事件能通过严格解析和生命周期核对，不能作为非零部分进度验证。完整epoch与33/33终末不等于外部writer-end或recording_status保证；恢复工具仍将recording_status标为unverified。trusted_root_fixture=true，unprivileged_validation=false；固定Linux/amd64、初始namespace、受控cgroup、普通loopback TCP，无NIC或宿主目录共享。其他内核/平台、生产降权、休眠和SSH退出仍未验证。

## PID复用正式边界（2026-10-26）

按[独立边界契约](../../specs/001-hoopa-proxy-manager/contracts/source5-formal-boundaries-v1.md)，先完成2条准备，再用同冻结镜像登记并完成2条正式边界，结果2/2通过。数值PID同为30000，独立rawstat启动时刻分别469、499 tick，实际cookie分别1、4097；两条连接的source5启动身份、flow ID、call token及来源记录分开。第一条完成33/33终末并回收后才创建第二个实例，第一closed序号早于第二opened；HTTP走node:a、SOCKS5走node:b，均33/33。

机器边界记录（本地原件 `../../experiments/mihomo-events/source5-identity-pidreuse-validation.json`，不随Git上传）保存两条原始oracle、truth屏障、PID分配与关闭顺序、完整host-check和37项摘要引用；正式run为13帧、25导出、1完整epoch。独立复核更正了口头提供的cookie：准备批次是1/3，正式实际为1/4097，以各自原始记录为准。该结果验证受控的两次PID复用，不表示穷尽PID回绕压力测试。两个边界槽位独立于正向100，formal_g01_new_samples=0；T127尚需缺凭据、权限及三值规则边界。

## BPF管理权限正式负例（2026-10-26）

独立准备通过后，同冻结镜像的正式单例1/1通过。原始receipt记录PID70/start253，在初始user namespace中降为UID/GID65534、清空groups和五类capability、设置no_new_privs；原有unprivileged_bpf_disabled=2未改动。直接对内核发出有效的HASH BPF_MAP_CREATE（key/value均8字节、1项、flags0、256字节零尾请求），实际返回EPERM，没有获得map FD。7个原始导出及归档摘要已独立复核。

机器权限记录（本地原件 `../../experiments/mihomo-events/source5-identity-permission-validation.json`，不随Git上传）保存完整降权/调用receipt、检查结论与18项引用。这关闭的是本次预登记的BPF管理操作权限负例，不表示普通应用不能使用有权限的collector，也不证明source可见性、特定内核拒绝分支、生产部署或更广的G07授权矩阵。该单例不产生流量，不加入100正向分母；T127仍需其他边界完成。

## 缺失执行文件凭据的正式边界（2026-10-26）

完成同镜像准备后，正式登记的一条基线与一条故障连接2/2通过，7帧、28导出、1完整epoch。正常基线保留33/33终末；故障客户端PID87/start491/cookie4097在独立connect前登记后建立TCP，停留于未发送代理协议的屏障。核心PID76的7个线程提前停止；特权guest父进程独立重开同map ID的可写FD，仅删除该cookie的exe_records，确认ENOENT且原112字节connect记录未变，关闭两个新FD后恢复核心。

恢复后Owner状态为admitted=1、rejected=1、revoked=true，核心degraded；故障socket关闭，且无该cookie的opened、线路或来源终末。机器缺凭据记录（本地原件 `../../experiments/mihomo-events/source5-identity-missing-proof-validation.json`，不随Git上传）保留原始连接与删除证据、source_bytes/source_terminal=null和40项摘要引用。客户端确实没有协议/应用写入，这与缺少来源账不同。负例slot中的SOCKS5/beta.test只是计划标签，并未观察到该代理请求或域名访问。

第一版准备因truth局部变量覆盖核心登记而在删除前失败，原0/2保留；修正位于独立lab，仅改局部变量名，并重新准备和登记。本次验证的是受控特权注入后的保守拒绝，不是一般用户map修改能力、特定错误原因API、所有hook丢失恢复或生产权限部署。两个正式边界槽位不加入100正向分母；T127尚待三值矩阵及整体复核。


## 三值规则正式边界与最终门槛（2026-10-26）

七类分别经过同冻结镜像的14条准备和新登记的14条正式连接；正式7VM、75帧、168导出、7完整epoch均通过独立复核。机器记录（本地原件 `../../experiments/mihomo-events/source5-identity-threevalue-validation.json`，不随Git上传）保留14条原始oracle、执行文件字节、来源事件、只读恢复及330项引用；正式摘要SHA256为`6c8caa90be960be3bab45481432d0b4c6b300a577f72c394f6e59fab67e753eb`。

| 原始字段与条件 | HTTP CONNECT及SOCKS5结果 |
| --- | --- |
| 目录含非法UTF8，程序basename有效，按NAME匹配 | node:a，2条33/33 |
| PATH未知，或basename本身非法UTF8导致NAME未知 | 4条规则拒绝，来源closed 0/0 |
| AND的DOMAIN为false、PATH未知，两种顺序 | 后续MATCH node:b，4条33/33 |
| AND的DOMAIN为true、PATH未知，两种顺序 | 4条规则拒绝，来源closed 0/0 |

规则拒绝的八条都先admit并有opened，之后是setup失败、REJECT和closed 0/0；客户端已发完整代理请求，没有应用字节。与缺凭据未admit的source=null不同。固定原始非法字节、配置、完整事件链及已审0017实现共同支持三值语义，通用`unknown_or_route_failure`文本本身没有独立识别内部原因。空fixture accept只表示没有观察到夹具接受连接，不证明零SYN。超时不算通过。

[只读总门槛](../../experiments/mihomo-events/source5_identity_gate.py)检查固定acceptance路径/SHA、五份公共报告、原始引用文件大小/摘要、独立分母和旧失败。它不会启动VM或执行证据提供的代码；默认缺少两项环境变量时Go入口明确skip，部分配置则失败，缺少private证据也不能通过。实际启用`HOOPA_SOURCE5_ACCEPTANCE`和`HOOPA_SOURCE5_ACCEPTANCE_SHA256`后，`TestSource5AcceptedIdentityBundle`定向race为1通过、0跳过，708份文件完整性匹配。Python文件/变异检查14通过；准备阶段错误要求permission报告携带core hash已修正并保留v1记录，该权限实验未运行核心，不能补造绑定。

最终只接受固定Linux/amd64受限source5的连接发起者口径与上述有限矩阵；原0/100、首轮60/100和缺凭据准备0/2保持失败。恢复、真实Agent在新核心上的完整工作流、生产降权、其他平台、SSH/休眠和性能预算均仍有后续验收，基础管理/CLI/TUI也尚未交付。
