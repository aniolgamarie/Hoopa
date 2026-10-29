# 受控非特权客户端的实际转发与权限拒绝

2026-10-25。按[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-unprivileged-client-v1.md)，在无网卡、无宿主共享的固定6.18.54.1/amd64隔离VM中，两个降权客户端已完成原入口身份、两线路、双向33B及终末记录核对；三个类别的权限拒绝均有实际errno。详细输入、原文摘要和限制见验证记录（本地原件 `../../experiments/sourceidentity/unprivileged-client-validation.json`，不随Git上传）。

## 实际结果

v2运行 `vm-unprivileged-client-8aufubiw` 的VM、提取、严格checker均exit0；38个导出和归档SHA核对。138项离线检查与实际两条连接分开计数。使用原0013核心 `476d5788…05467b` 和原组合BPF `1bf999df…59500`，没有使用准入计时overlay。

| 原始连接 | PID/TID | start ticks | client / accepted cookie | 实际节点 | forward ready | 终末上传/下载 |
| --- | --- | --- | --- | --- | --- | --- |
| HTTP / alpha.test | 84 / 84 | 423 | 4097 / 4098 | node:a | 11ms | 33B / 33B |
| SOCKS5 / beta.test | 89 / 89 | 465 | 4099 / 4100 | node:b | 31ms | 33B / 33B |

独立exec wrapper先入leaf，清空supplementary groups、bounding/ambient及其余capabilities、切换全部UID/GID并设置NNP，再exec固定客户端。wrapper与exec后client保持相同PID/start；这是同一实例的权限变化，不能当成两个进程。客户端self proc及PID1独立原始proc记录均证明四个UID/GID坐标为65534、Groups为空、五组capabilities为0、NoNewPrivs为1。PID1核验后才创建root控制的release；创建目标socket及connect严格位于release之后。

两条296B exe记录的token为1/3，路径 `/bin/python3`、程序名 `python3`，独立oracle dev2/inode8与原始connect对应；完整13帧含两closed及complete epoch_closed，无gap、两尾段均0。发送33B后SHUT_WR，收到精确33B及EOF。核心77持有20个移交FD且恢复CLOEXEC，最终admitted2/rejected0/revoked=false，四项采集诊断均observed0；客户端退出0并reap，核心SIGTERM0。

## 实际权限边界

两客户端各自观察到相同结果，均在目标socket创建前完成：

| 操作 | 结果 | 可据此说明的范围 |
| --- | --- | --- |
| root与leaf的cgroup.procs写打开 | 两处均EACCES(13) | 没有执行write，不能取得这两个控制文件的写FD |
| pidfd_open(77) | 成功返回自有FD3 | 打开pidfd不等于获得核心文件描述符 |
| pidfd_getfd请求核心原lifecycle FD219 | EPERM(1) | 没有复制该核心FD；只关闭自有pidfd一次并确认EBADF |
| BPF command30 / 4B link_id5 | EPERM(1) | 请求编码正确但被拒绝，不声称已执行内部查找或卸载 |

实际目录原文显示 `/`、`/work` root0755，`/work/out` root0711；core-home、trace、bootstrap维持root0700。各oracle与socket子目录为65534:65534/0700，release为root0444，应用不可写其父目录。权限探针没有取得意外管理FD，也没有执行detach/update、cgroup迁移或sysctl变更；wrapper此前入leaf的登记步骤另有记录。

## 保留的失败及未证明事项

首轮 `vm-unprivileged-client-8brmwq7j` 的VM/extract为0、checker为1：核心已ready，但创建客户端前的祖先权限断言失败，clients为空、无flow，核心TERM0。该版在断言后才保存layout，具体哪个祖先的实际mode/owner不在证据中，不能事后猜定。v1镜像、源码及运行原文保留。

v2显式设置并归档根目录及work/out权限，并在校验前保存实际layout；只改变guest保存顺序及镜像目录属性，其余guest文件内容逐项保持。新增只读cpio回归证明v1没有显式根目录项、v2具有登记权限，随后新VM通过。v2镜像41987821B，SHA `84c4820bd490824e5cfd56dd6a3449bdbb1a820675fb7d8d904fb22ab7babd68`。两项更早的纯fixture准备错误也保留，未改写为运行成功。

该批只证明这些固定客户端和管理接口；UID/capability降权及启动时namespace检查没有证明动态namespace不变。源码已确认需要[建连时user/mount范围守卫](../../specs/001-hoopa-proxy-manager/contracts/connector-namespace-guard-v1.md)，新增候选另行实现验收。本结果也不替代生产launcher部署、任意特权干扰防护、其他内核/平台或长期容量验证。

正式实例/名称/路径能力仍为false/unverified，旧v3/v4与PROCESS配置拒绝保持；T132/T137未完成，139项任务中21项完成，正式G01新增0、旧0/100及P0/P1未放行不变。
