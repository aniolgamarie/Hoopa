# 建连时 user/mount 范围守卫的有限验证

2026-10-25。按[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-namespace-guard-v1.md)，三个独立无网卡、无宿主共享的固定6.18.54.1/amd64 VM均完成运行、canonical提取和严格checker，三阶段均exit0。本批3VM无运行失败，共4条正常流、27帧、138个导出和3个归档；文件长度及SHA独立重算，详见验证记录（本地原件 `../../experiments/sourceidentity/namespace-guard-validation.json`，不随Git上传）。

## 制品与正向结果

使用[0014候选](../../experiments/mihomo-events/core-namespace-guard-candidate-manifest.json)：核心51271973B，SHA `97d0a450202a94cee40a78d32a747865534bee1ef06b8d64efaf24819bb84167`；新scoped BPF SHA `df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49`。镜像42009541B，SHA `cdea655bd5e1374905d3a4f9e5847d559301acc2d39889ebe800187be4792248`。固定上游v1.19.26 commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`；无ldflags的默认版本输出不能替代源码版本。

| VM模式 | 原连接PID/TID | 协议与实际节点 | ready | 终末上传/下载 |
| --- | --- | --- | --- | --- |
| positive | 86 / 86 | http / node:a | 11ms | 33B / 33B |
| positive | 91 / 91 | socks5 / node:b | 31ms | 33B / 33B |
| user-namespace | 84 / 84 | http / node:a | 11ms | 33B / 33B |
| mount-namespace | 84 / 84 | http / node:a | 11ms | 33B / 33B |

四条原连接的296B记录均为consumed1、路径 `/bin/python3`、basename `python3`，PID/TID/cookie、leader start和BOOT区间与各自独立proc oracle（dev2/inode8）相符。33B发送后SHUT_WR，再收到精确33B及EOF；实际node/target按payload独立对应。各来源严格v4解码与只读重放均为complete epoch、closed尾0、无gap，记录状态仍unverified。

三个核心均采用 `scoped-executable-online`、严格handoff-v3，持有21个唯一移交FD并恢复CLOEXEC。bootstrap在任何link前冻结两配置，exec-intent另行保存实际只读map lookup的24B scope原文；每个VM的generation不同，解析后的user inode均4026531837、mount inode均4026531832、schema1/reserved0，与该VM登记及原始namespace记录一致。原文hex与摘要逐场列在JSON中。

正例两顺序客户端及两负例的正常HTTP基线均先完整降权：全部UID/GID65534、Groups空、五组capabilities为0、NNP1，wrapper到exec后客户端保持原PID/start；PID1独立proc核验并release后才创建目标socket。各正常客户端实际观察root/leaf cgroup.procs仅写打开EACCES(13)、pidfd_open成功、自有pidfd_getfd请求核心原lifecycle FD220为EPERM(1)、BPF command30/4B link ID请求EPERM(1)；仅关闭自有副本，没有write/detach/update。`/`、`/work`为root0755，OUT为root0711，核心目录root0700、各独立应用oracle为65534:65534/0700。

## namespace确实改变后的拒绝

| 模式 | trigger PID / UID | 指定unshare结果 | inode变化 | 原native connect | initial followup |
| --- | --- | --- | --- | --- | --- |
| user-namespace | 89 / 65534 | CLONE_NEWUSER = 0 | 4026531837 → 4026532154 | -1 / EPERM(1) | PID92，EPERM(1) |
| mount-namespace | 89 / 0 | CLONE_NEWNS = 0 | 4026531832 → 4026532154 | -1 / EPERM(1) | PID91，EPERM(1) |

user负例的UID65534客户端实际成功进入新user namespace；mount负例仅由自有root夹具进入新mount namespace。各trigger的PID/start、PID/net namespace与source leaf保持，另一个namespace坐标不变；followup则保持全部原初始坐标。全部4次负向普通blocking native connect都返回EPERM，协议及应用写均0，没有新增node/target连接或opened。这个结果证明改变后建连被拒绝，不能写成namespace不能改变，也不证明非特权客户端拥有mount管理权限。

两负例的capture_fault原始观测均依次为0、0、1、2；exe fault bits及两个raw recursion misses均observed0。初始namespace followup仍被拒绝，符合sticky fault判据。Owner计数一直admitted1/rejected0/revoked=false、live gauge0；这些拒绝没有产生accepted来源flow，不能把BPF侧拒绝次数改写为Owner拒绝数。正例最终admitted2/rejected0/revoked=false、四项诊断全0。三个核心均SIGTERM0，各自拥有的客户端正常退出/reap。

每个负例完整保留正常基线的六帧原文及hash，两个负向阶段未追加来源帧；最终仅追加complete epoch_closed成为7帧。没有将活动文件可见性当作fsync证明，也没有推断未暴露的单一内部拒绝分支。

## 准备记录与限制

最终227项纯/fake检查与先前221项准备结果分开保留，不相加计为运行样本。首次准备中的一个负例fixture错误、成功但元数据仍继承旧包的v1镜像均保留；最终使用独立v2镜像。入口fake执行到实际admission保存布局和Popen边界，并证明旧 `/work0700` 会在spawn前拒绝；这些检查没有被算成真实VM。更早的[降权首轮失败](connector-unprivileged-client.md)及旧0013材料保持原样。

本批只验证固定内核和受控原生IPv4 loopback路径上的调用线程user/mount范围；普通CO-RE读取不是跨全部状态的原子快照，零遗漏诊断也不能证明hook完整性。路径按调用者fs root解析，mount inode不单独固定全局根目录；任意特权干扰、生产launcher、其他namespace/内核/平台和长期容量仍不在本结果范围。source v4及旧process unknown保持，正式实例/名称/路径能力仍false/unverified，不开放PROCESS规则。T132/T137未完成，139项中21项完成；正式G01新增0、旧0/100及P0/P1未放行不变。

独立harness及实际runtime review均已完成；runtime复核138/138导出、3归档及原始来源/namespace/诊断，没有critical/high证据矛盾。报告路径与摘要列在验证JSON中。
