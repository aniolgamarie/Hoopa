# 组合核心的范围拒绝与后续连接 v1

2026-10-24，运行前登记；T132有限负向子步骤。前置为[组合正向实测](../../../docs/validation/connector-executable-core-positive.md)和[五类原连接边界](connector-executable-core-edges-v1.md)。固定001–012核心、组合对象、20FD handoff及6.18.54.1 guest；无网络设备、宿主共享或宿主BPF/cgroup/sysctl更改。旧对象的范围拒绝结果仅供编写夹具，不算本组合通过。

每个场景新VM、新采集世代，顺序固定为：一个source叶内普通HTTP基线流完成33/33并关闭；一次受控范围外原native connect；一个source叶内、初始namespace、新socket/新tuple的普通native connect。三次均指向原入口127.0.0.1:18993。先注册各角色、预期与新制品摘要，再运行；客户端scope、namespace、cookie、PID/start和调用错误独立记录。

| 场景 | 第二次连接的边界 | 必须独立观察的前提 |
| --- | --- | --- |
| `outside-source` | 同guest初始namespace、source叶之外 | root覆盖实际存在，客户端当前cgroup与已注册source叶不同，目标确实落入入口过滤条件。 |
| `pid-namespace` | 保持source叶，在新PID namespace内连接 | 初始坐标中的wrapper/后代关系、实际NSpid与新namespace inode；不是用包装进程的PID冒充调用者。 |
| `net-namespace` | 保持source叶，在新net namespace中创建socket | 私有loopback已启动，实际socket netns cookie不同；不能把未配置路由造成的错误当捕获拒绝。 |

第二次和第三次connect均须明确返回EPERM，协议与应用写入均为零。第三次必须证明自己属于正常source叶和初始namespace，使用新cookie/tuple，排除重试旧socket或四元组冲突造成的拒绝。所有原socket由自己的客户端关闭；包装进程负责有界wait/reap。测试期内不重建loader、core或map，不清空fault，不切换generation；基线原始opened/ready/closed及33/33终末保持不变，不出现第二个flow。

本轮以行为证明“同世代后续正常连接也被拒绝”，不借没有读取的BPF map数值推断内部计数，不宣称定位到某一具体内核分支。core的实验admission计数只覆盖到达原accepted/Admit的连接；预计admitted=1/rejected=0/revoked=false。它不能反映在connect钩子被拒绝的次数，也不能据此声称采集健康。保留原始status观察并明确这一区别，不能修改结果填成owner已经撤销。

验收仍要求20FD与CLOEXEC、guest/对象/core/decoder摘要一致，基线v4 executable证据与原oracle一致、实际node:a/ready/33字节整数终末、完整7帧epoch和tail=0；独立代理/目标记录只含基线token。核心SIGTERM退出0，原始导出由canonical extractor核对，主机checker逐项确认原字节。准备测试和实际VM/flow分别计数，任何错误阶段、未知errno或不完整材料都记本轮失败。

这不是跨namespace支持、自动恢复或生产可用性验收。拒绝会影响整个有限世代；新资源恢复、tuple/TFO/非native路径、缺hook、map容量与长期回收分别验收。原始FD复制/关闭不会使固定内核rawTP或LSM link在core仍持有时自动detach，不使用该错误前提注入缺hook。正式能力和任务门槛保持。
