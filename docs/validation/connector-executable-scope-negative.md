# 组合核心的范围拒绝与后续连接

2026-10-24。[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-scope-negative-v1.md)的三个独立VM场景均通过原始导出和主机断言。每场基线转发完成后，范围负例与同世代的正常后续连接都在connect返回EPERM，零协议/应用写入；基线的身份、质量与终末记录完整保留。机器记录（本地原件 `../../experiments/sourceidentity/executable-scope-negative-validation.json`，不随Git上传）保存逐场证据摘要与限制。

这是有限范围拒绝验收，同时揭示了当前可用性限制：一次捕获故障会使该采集世代持续拒绝新连接。尚未实现或验收生产环境的自动恢复，不能把这项结果写为跨namespace支持或完整身份能力。

## 实际结果

81项离线准备检查后执行3次VM，分别完成VM→canonical extractor→host checker，三阶段均exit0。共有3条基线流、6次被拒绝的目标connect、21帧来源事件；73个导出文件（23/25/25）逐项重新核对字节数和SHA256。离线检查不算实际连接样本。

| 场景 | 基线PID/ready | 触发连接 | 正常后续连接 | 来源结果 |
| --- | --- | --- | --- | --- |
| source叶之外 | 84 / 12ms | 初始PID87，guest根cgroup，EPERM | PID88，回到source叶、新socket/31003，EPERM | 原flow 33/33、7帧、完整结束 |
| PID namespace不同 | 86 / 11ms | 初始PID91、namespace内PID1，NSpid `[91,1]`，EPERM | PID92，初始namespace/source叶、新socket/31003，EPERM | 同上 |
| net namespace不同 | 84 / 11ms | PID88，新netns inode和socket netns cookie，私有lo已启用，EPERM | PID90，初始netns/source叶、新socket/31003，EPERM | 同上 |

基线使用HTTP入口，实际经alpha.test/node:a；每条原v4 opened的296B executable记录匹配原client cookie、TGID/TID、正整数call token与BOOT窗口。独立`/proc/self/exe`前后oracle为`/bin/python3`，dev2/inode8；核心不会读取该oracle来准入。真实ready、同attempt/actual_connection_ref、终末各33B及段守恒分别核验。客户端提前SHUT_WR后收足回复和EOF；这一基线操作有原始记录。

范围外source确在guest根cgroup，followup进入已注册source叶。PID namespace场景独立核对wrapper、初始/可见PID、NSpid和namespace inode；net namespace场景独立核对不同netns cookie及lo设置。后续连接均由另一进程创建新cookie、不同四元组，排除了重试旧socket造成拒绝的解释。包装进程与客户端有界等待、reap，核心SIGTERM退出0。

## 状态接口的实际含义

三场在基线之后、触发之后、followup之后均观察到当前连接gauge为0，owner准入计数为`admitted=1, rejected=0, revoked=false`，控制状态仍为`running`。这与代码一致：计数只包含到达原accepted socket、执行Owner.Admit的连接；在connect钩子被拒绝的连接没有进入此计数。

因此`revoked=false`不能证明内核捕获仍健康，`rejected=0`也不表示没有连接被拒。正式诊断必须区分入口运行状态、accepted准入结果与捕获健康；后续需要补可读的捕获状态和恢复行为。本轮没有读取原始BPF fault计数，机器结果明确标为未观察，只报告“同世代后续正常连接也被拒绝”的实际行为，不冒称观察到了计数递增或某个唯一内核分支。

## 固定输入与检查

- 核心为[001–012候选](../../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)，SHA256 `4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27`；组合对象为`1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`。
- 镜像41,989,611B，SHA256 `81c7704703b14f740cbc814f0a1274a012bde3962d14030f783c36669442620d`。实际guest为6.18.54.1，BTF内容、BPF LSM、20FD handoff及恢复CLOEXEC均核对。
- VM没有NIC或宿主共享，不修改宿主BPF、cgroup或sysctl；诊断与原始证据分用UART和virtio通道。每场使用独立资源世代。
- 原帧由v4严格解码，再用只读sourceinspect核对完整epoch、closed终末与tail=0；未用事后派生值改写来源帧。
- 独立scout只读复核三场稳定证据、73项导出与namespace/后续连接身份，未发现critical/high矛盾。主代理完成夹具、实际运行和最终验收；未重跑旧样本来增加分母。

首次私有源码生成因原fixture词序不同触发保护断言，尚未生成镜像；首轮离线变异检查发现NSpid首/尾一致性校验遗漏，修正谓词后81项通过。这两项准备失败保留，本批3次实际VM没有失败。此前0011正向整轮失败、旧名称覆盖/旧快照反例及G01原0/100均保留。

## 仍待完成

本批不覆盖四元组复用、TFO、非native ABI、缺hook、并发连续性、核心崩溃后的新组合恢复或长期map回收。后续按[连接负向契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-connection-negative-v1.md)继续；未验证的平台、6.6、真实SSH/休眠与性能预算仍未通过。

正式identity/name/path能力仍false/unverified，PROCESS规则仍拒绝，G01新增正式样本0。T132/T137未完成，139项中21项完成，P0/P1尚未放行。
