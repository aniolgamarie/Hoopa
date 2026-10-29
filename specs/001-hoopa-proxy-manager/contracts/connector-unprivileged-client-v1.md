# 受控非特权客户端的身份正例与管理能力拒绝 v1

2026-10-25，运行前登记。按[ADR010](../../../docs/adr/010-restricted-identity-profile.md)补齐一个具体部署前提：将可管理 BPF/cgroup 的实验 bootstrap/core 与应用客户端权限分开。只在自有无网卡/无宿主共享、有界隔离 VM 内执行，不建设常驻生产 helper，不修改宿主权限。使用原001–013核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`，原组合 BPF `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`，固定6.18.54.1/amd64；不用准入计时专用overlay制品。

## 固定流程与权限边界

每次只起一个新VM，两条顺序流：HTTP/alpha.test/node:a、SOCKS5/beta.test/node:b。PID1先创建 root 所有的专用 cgroup leaf，完成原20FD接管、对象核验和核心ready，再逐个启动自有独立 Python wrapper。不得在已有多线程guest中使用 Python preexec_fn 执行降权；wrapper是单独exec后的进程。

wrapper必须确认专用guest/root、初始namespace、精确cgroup路径和自己的PID1父关系，记录原PID/start。先将自己加入指定leaf，确认membership，再清空supplementary groups；在仍有CAP_SETPCAP时逐个清空capability bounding set（CAP_SETPCAP最后），清空ambient，禁用keep-caps，setresgid/setresuid全部改为65534，capset将effective/permitted/inheritable清零，设置PR_SET_NO_NEW_PRIVS=1。所有调用逐项记录成功或errno，任一失败拒绝exec，不能把部分降权当成功。

随后仅exec固定 `/bin/python3 -S -B /work/client.py` 与登记argv，使用明确的最小环境。新客户端exec后再次核验四个Uid/Gid坐标、空Groups、CapInh/Prm/Eff/Bnd/Amb全部0、NoNewPrivs1；不能只检查euid。wrapper与exec后client的PID/start必须相同；两条独立流应有不同实例/cookie。初始PID/user/net namespace inode由可信PID1在spawn前记录并传入，非root只读取自身namespace对照，不能把无法访问 `/proc/1/ns` 当作初始namespace已证。

输出只写已打开的stdout/stderr及各自预建、归UID65534所有的0700 oracle目录。PID1保留 `/work/out` 所有权，仅给遍历权限，核心配置、trace及bootstrap目录仍为root私有；客户端不得获得这些目录或父目录的写权。不凭空添加用户数据库依赖。oracle的路径、mode、owner和祖先遍历范围由guest记录；自己proc、exe/socket/comm观察必须仍然成功。

在创建目标TCP socket前，客户端写出独立等待marker，并等待PID1受控release。PID1从真实 `/proc/<owned pid>/status`、stat、cgroup、namespace再次保存并核验同PID/start、UID/GID、全部capability和NNP；确认后才释放。release文件由root创建，应用只能观察存在，不可写父目录或伪造release。失败路径只清理尚未reap的自有Popen。

## 实际权限拒绝探针

客户端在降权后、目标connect前执行以下有界操作；不以配置文字或capability位代替实际结果：

| 探针 | 精确操作 | 预期 |
| --- | --- | --- |
| cgroup控制 | 分别对root与source leaf的 `cgroup.procs` 执行 O_WRONLY\|O_CLOEXEC open；绝不write | 两者EACCES；记录实际root owner/mode与路径 |
| 核心FD | pidfd_open(434)打开仍存活自有核心PID，再pidfd_getfd(438)请求原handoff中指定的 `hoopa_lifecycle` link FD | open成功、getfd返回EPERM；只关闭自己的pidfd一次 |
| link对象获取 | BPF(321) command30、4字节 little-endian原link_id attr，即BPF_LINK_GET_FD_BY_ID | 返回EPERM；EINVAL/错误attr不能代替权限拒绝 |

若意外获得FD，立即关闭新副本、保存失败，不能据此继续实际detach/update。原核心FD不关闭、不修改map、不迁移进程、不改sysctl/ftrace。不能将pidfd_open成功误写为取得核心FD；BPF命令的EPERM也不被说成已执行了link查找或卸载。

## 原始连接与终末判据

权限探针与父进程核验后，两客户端分别在受控leaf中以非root身份完成native connect、原exe/socket独立oracle、真实代理握手、33B发送后SHUT_WR、精确33B回显/EOF。原opened必须匹配exec后的客户端PID/TID、leader启动身份、cookie与296B exe记录，不能改成root wrapper、PID1或核心；wrapper exec没有创建新的PID/start，报告仍须明确其权限变化时点。

每条原始线路必须为登记node:a或node:b，ready具有实际协议证据；同一core/source epoch/capture generation，两个closed/tail0、最终完整13帧、无gap；admitted2/rejected0/revoked=false、四项捕获诊断observed0。所有客户端退出0/reap，核心SIGTERM0。源目录/核心对象和权限不因拒绝探针改变。额外身份字段只属于实验oracle，不回填旧wire或提升正式capability。

准备验证包括Python3.8语法、真实guest.main假环境启动、降权顺序/失败不exec/副本cleanup纯fake、原fixture与权限原文的负向变异；实际权限结果只能由VM运行验收。保留每次准备或运行失败，所有条件完整才可报告本批通过。

该实验仅证明固定受控客户端在这个guest中不能通过所测接口获得管理能力，不能冒充生产launcher部署、任意本机恶意用户隔离、阻止受信root管理员变更、全部权限侧信道或全平台支持。T132/T137、新正式G01样本和P0/P1门槛保持独立裁决。
