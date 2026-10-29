# US4 应用选路核心有限验收

2026-10-28/29，T048 的原生核心前置资格已完成，T048 整项保持未完成。固定 Linux/amd64 静态核心为 `6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b`，51,636,035 字节；批准范围与原来源核心 c90 分开。见[批准记录](../../experiments/mihomo-events/approved-app-core-v2.json)、[完整证据索引](../../experiments/mihomo-events/core-app-ordered-v2-manifest.json)和[增量核心补丁](../../experiments/mihomo-events/patches/0021-app-ordered-ip-unknown.patch)。

## 修复及实际区别

解析后仍无 IP 时，受保护 IPCIDR 现在产生 unknown；应用选路启动在转发前单独启用三值规则分派，避免无计量入口没有遥测 FlowID 而继续使用布尔匹配。普通布尔 Match 和 basic-v1 启动保持独立，不因此授予原进程身份或进程条件能力。管理 DTO/端点仍为 v2，模式为 basic-app-routing，能力列表及固定二进制单独登记。

最终 image04/VM03 只有 loopback，无网卡和宿主共享目录。实际 DNS 收到每阶段6个 A 查询，返回规定的 NXDOMAIN、127.0.0.2 或192.0.2.1。新核心8个子场景全部通过：前置 IP unknown、unknown+true两种顺序、unknown+false两种顺序、字面 IP 确定不匹配，以及解析成功的匹配/不匹配。4个拒绝均收到终末502；核心退出并等待代理服务器/处理器收束后，上游 CONNECT和GET均为0。4个放行到指定HTTP CONNECT节点。相同VM中的旧c90仍在3个unknown场景失败、其余5项通过。这里的应用上下文是命名入口，不能据此填写原进程PID。

初版候选 aef994 的 helper 单元测试通过，但VM01仍错误放行：实际无FlowID路径没有调用三值方法。失败二进制、1027份原源码及日志均保留。修正分派后VM02先通过，VM03又收紧拒绝观察；这些不叠加成额外场景。build02遇到/tmp空间不足，改用仓库私有构建临时目录后的build03成功；未清理别人数据。image02仅为未启动的旧镜像重复物，不算验收。

## 其他验证与边界

既有实际原生来源/控制面测试在此二进制上normal/race各4顶层、13含子测试通过，涵盖有限构造/快照、关闭入口、认证、实际探测/组选择、控制历史与v1兼容。另一个实际状态测试验证v2、新mode、精确6项能力、配置hash和原生快照。生产OpenExecutable接受私有副本；补丁重建1028份冻结源码完全相同，另一目录构建得到相同ELF。

helper normal/race各14含子测试，分派normal/race各14，host codec normal/race各199，以及相应vet通过。native sourcecontrol/sourceprofile没有测试文件，日志中两个package skip如实保留；它们的host codec和实际ELF分别验证。没有把这些分批结果称作完整系统race验收。独立审查由scout实际gpt-6-luna/high执行，root负责核心修复/资格判断；executor实际gpt-6.1-sol/medium另行实现产品模块。

这不等于完整应用绑定、Plan/Apply、故障回滚或管理器死亡的产品验收；它们继续T048–T051。未授予原进程规则、计量、真实业务延迟、全部DNS故障类型、全部远端协议握手、热重载、其他平台或性能预算结论。实际DNS故障校准使用NXDOMAIN。

## 产品批准登记与状态身份绑定

T048 registry模块索引（本地原件 `../../experiments/gost-mihomo/us4-app-registry-validation.json`，不随Git上传）固定新批准工厂及17项scoped输入。普通Inspect和termination每次读取status均逐项比较本地已绑定的protocol/schema、mode和完整有序capabilities，再核run/generation/config/version/snapshot。修复前真实回环脚本复现10个失败子例；修复后定向normal/race各28顶层/244含子测试PASS，0fail/skip，vet通过。旧basic/c90批准与其原证据SHA不变。此组是实际HTTP脚本与合成core元数据测试，未执行app ELF；核心实际证据仍见上文独立资格。T048整项继续未完成。
