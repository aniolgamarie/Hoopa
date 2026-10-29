# 固定核心 connector 边界矩阵 v1

2026-10-24 下一阶段预登记契约；本文件定义判据，不记录尚未执行的结果。承接[核心移交](connector-core-handoff-v1.md)、[v3事件](source-connector-v3.md)和[上一阶段有限验收](../../../docs/validation/connector-core.md)。继续T132，正式门槛和139/21不变。

使用已固定的001–009核心hash `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`、同一online BPF对象/libbpf和隔离6.18来宾拓扑。新harness/image及client须重新固定摘要，各次运行先登记期望和原始客户端oracle；不把oracle交给核心resolver，不覆盖旧运行。基础进程名和路径仍不提升能力，结果最多是有限实验观察。

## 首批独立客户端变化

1. **thread**：工作线程执行原始connect、采样和代理协议；独立记录TGID、实际TID及leader启动ticks。核心connector必须归进程TGID及leader启动身份，caller TID对应工作线程，不拿线程名代替进程名。
2. **fork-writer**：父进程原connect并采样，子进程对该原TCP执行代理握手和payload；归属保持父connector。记录writer PID与connector PID不同，完整核对终末和实际线路。
3. **scm-writer**：子进程先关闭fork继承的原TCP FD，再通过SCM_RIGHTS取得该socket，核验cookie/namespace/两端点后才代理握手和payload；不得以仍保留的继承FD伪装成功转交。归属仍为原parent connector。
4. **rename-after-connect**：有序设置受控leader名、connect、记录原实例，再改名后发代理请求。核心仍保存connect时采集的comm原字节；改名后的观察另列。该受控串行案例不证明并发改名的原子性，comm能力继续unverified。
5. **exec-fd**：connect并采样后仅为该原socket开启继承，在同PID通过exec重新进入测试程序，再核验PID/start/cookie/端点并发送代理请求。core记录connect时实例，exec后的名字只作后续观察，不改写历史。oracle文件为私有0600，不含真实用户认证信息；exec失败清理自有FD，不能泄漏给不相关进程。

各mode须有界完成真实HTTP CONNECT或SOCKS5认证与握手、33B请求/回显及EOF；只允许loopback固定fixture，不从父代理环境推导目标。默认CLI没有显式开启时不创建socket。独立client输出包含原PID/TID/启动ticks、before/connect/after comm、writer PID、协议/目标、双socket信息和echo结果。清理仅操作当前调用拥有且未reap的子进程，不能仅按复用的数值PID发送信号。

## 核对与停止条件

先跑无网络定向测试，再将新脚本固定进独立guest image；宿主不加载BPF、不修改cgroup。guest按前一阶段入口scope/namespace和真实协议执行，注册期待的mode/客户端数/线路。读取原始来源帧，经严格v3解码、epoch状态机与来源恢复核对，再按独立oracle匹配原connector；不能只看应用echo或status admitted数。

任何PID/start/cookie/端点错配、scope绕过、原帧缺失/冲突、错误flow/线路或整数终末不符均保留失败并停止该候选扩大验收。受控field对齐不使实例/名称/路径整体自动成为verified。首次失败允许修正harness自身的已证缺陷，但须新登记，旧运行不得覆写。

并发/早退/PID或tuple复用、跨namespace、漏采和活动来源crash/gap等其他T132场景仍在任务原验收范围内；本首批不替代它们，也不放行T137/T136或正式G01。管理器生命周期不由client/core退出案例冒充。
