const detail = (flow, mechanisms, invariants, components, limitations) => ({
  flow,
  mechanisms: mechanisms.map(([title, text]) => ({ title, detail: text })),
  invariants,
  components,
  limitations
})

export const manualDeepDive = {
  overview: detail(
    ['读取 Manager 状态', '聚合机器、Agent、实例和集群台账', '读取最新心跳、告警和任务统计', '生成只读快照', '下钻异常对象'],
    [
      ['读模型聚合', '概览只读取持久化台账、最新心跳、告警和任务，不在页面加载时向生产主机执行命令。'],
      ['健康语义分层', 'Manager running、Agent online、机器可达、MySQL health 与任务 success 是五种状态，不能用一个绿色状态互相替代。'],
      ['快照与时序分工', '概览用最新快照获得低延迟；趋势、峰值和故障前后变化必须进入性能页读取持久化时间序列。'],
      ['异步结果', '安装、备份和架构调整被接受只表示任务已创建，最终结果以任务步骤、事件和目标状态收敛为准。']
    ],
    ['概览查询不得改变远端状态', '任何远端变更都必须关联任务或运行记录', 'Agent 在线与 MySQL 健康分别展示', '过期快照不得解释为实时值'],
    ['ManagerRuntimeService', 'HeartbeatService', 'Machine / Cluster Service', 'TaskService', 'AlertService'],
    ['跨组件读数不一定属于同一数据库事务时刻。', '网络抖动会造成短时滞后，应同时检查采集时间。']
  ),

  machines: detail(
    ['选择 SSH 凭证', 'SSH 与平台预检', '保存机器台账', '安装或接管 Agent', '等待注册和心跳', '采集资产并分配集群'],
    [
      ['SSH 只负责引导', 'SSH 用于首次安装、离线修复和卸载；Agent 上线后日常采集与运维改走任务通道，缩小长期远程登录面。'],
      ['平台识别', '预检识别操作系统、CPU 架构、systemd、目录权限和网络条件，避免写入不匹配的静态二进制或服务文件。'],
      ['凭据隔离', '凭据由 Manager 保存和使用，列表与详情不回显密码、私钥或口令；浏览器通常只提交 credential_id。'],
      ['删除语义分级', '保留既有 Agent、保留 MySQL、仅解除纳管、卸载 Agent 和卸载 MySQL 是不同影响等级，不会由“删除台账”隐式互换。'],
      ['静态与动态资产', 'CPU 型号、内存、磁盘等静态资产按任务重采；利用率等动态数据来自心跳，不会随编辑机器名称而变化。']
    ],
    ['同一管理地址不得重复纳管', '删除前必须展示登记实例和远端探测结果', 'SSH 秘密不得进入普通响应和日志', '解除纳管不得隐式卸载 MySQL'],
    ['MachineService', 'SSH credential store', 'Agent installer', 'TaskService', 'HeartbeatService'],
    ['Agent 离线时无法走任务通道，恢复或卸载要求 SSH 可达。', '静态资产只是最近一次成功采集。']
  ),

  agents: detail(
    ['连接 Manager WebSocket', '上报能力与启动标识', '周期发送心跳和指标', 'Manager 协调租约状态', '任务分发到类型处理器', '进度和结果串行回报'],
    [
      ['双通道', 'HTTP 心跳承载状态与指标，WebSocket 承载任务、步骤和事件；其中一个健康不证明另一个一定健康。'],
      ['心跳状态机', '默认 15 秒未更新进入 SUSPECT、30 秒进入 OFFLINE；连续失败可为 DEGRADED，连续两次健康后恢复，Manager 每 5 秒协调。'],
      ['防旧包覆盖', 'boot_id、stream_id、seq 和 uptime 用来区分 Agent 重启与连接流，迟到心跳不能覆盖新状态。'],
      ['多 Manager 重连', 'Agent 可轮换多个 Manager 地址，失败后约 3 秒重连，连接建立时重新上报 capabilities。'],
      ['类型化执行', '每个任务由明确 handler 并发处理；Reporter 统一串行化 JSON 进度、事件和终态，避免输出交错。'],
      ['动态采集配置', 'Manager 在心跳响应中返回主机/MySQL 采集开关、间隔与模块配置，Agent 无需重启即可收敛。']
    ],
    ['离线判定基于租约而非单次失败', '未登记端口的 MySQL 指标不得入库', '任务类型必须有受控处理器', '卸载不能依赖即将停止的 Agent 通道收尾'],
    ['Agent receiver / dispatcher / reporter', 'HeartbeatService', 'Lease reconciler', 'Task WebSocket', 'SSH recovery'],
    ['ONLINE 只证明 Agent 心跳，不证明数据库可服务。', '代理或防火墙中断长连接会造成任务通道反复重连。']
  ),

  clusters: detail(
    ['创建逻辑集群', '分配机器成员', '登记实例', '心跳发现角色与复制关系', '聚合拓扑、性能、告警和备份'],
    [
      ['逻辑边界', '集群名把机器、实例、告警、批量任务、备份和架构运行关联到同一运维范围，不等同于 MySQL 内部对象。'],
      ['拓扑读模型', '拓扑组合实例角色、复制来源、心跳、容量和指标；画布只表达期望或观测，不凭前端状态宣称远端已完成。'],
      ['成员一致性', '成员设置以 machine_id 为输入并逐项返回结果；清理仅修复不存在或失效的关系。'],
      ['作用域互斥', '集群级自动化、VIP 锁与架构锁以集群为键，阻止同一集群并发执行冲突拓扑操作。']
    ],
    ['机器成员关系应有唯一归属', '非空集群不能直接删除', '清理成员不得卸载软件', '实际拓扑必须来自心跳或执行校验'],
    ['ClusterService', 'MachineService', 'Topology handler', 'Heartbeat snapshots', 'Cluster locks'],
    ['复制权限或实例登记错误会导致拓扑边缺失。', '跨集群复制可被观测，但不自动改变逻辑成员。']
  ),

  'cluster-architecture': detail(
    ['获取集群锁并持续续租', '预检 Agent、MySQL、GTID、server_id 和网络', '候选节点排序', '冻结旧主并排空业务连接', '等待复制追平与 relay 回放', '围栏旧主并扫描 VIP', '全节点撤销 VIP，证明零持有者', '提升新主并重定向副本', 'L2 ARP 或 BGP 宣告', '连续两轮全节点唯一持有者证明', '一致性校验并恢复连接'],
    [
      ['候选选举与 GTID', '分数综合资格、GTID/relay 新鲜度、健康、人工优先级、延迟与风险；候选必须包含必要事务历史，延迟副本、重复 server_id 或非候选会被拒绝。'],
      ['旧主冻结与围栏', '切换前开启 read_only、super_read_only、offline_mode，终止非管理会话；围栏还可停止 MySQL、删除 VIP，并经 Agent 或 SSH 确认旧主不再接收写入。'],
      ['L2 ARP 宣告', '网卡按实例配置、集群默认、can_bind_vip 且 is_up、最后子网匹配的顺序选择；执行 ip addr add、link up、arping -U -c N，再读取地址验证。'],
      ['BGP/FRR 宣告', '在 loopback 绑定 VIP/32，经 vtysh 配置 local-AS、peer remote-AS、router-id、IPv4 address-family、可选 community route-map 和 network VIP/32。'],
      ['BGP 成功证明', '命令成功并不足够；还要验证 FRR 邻居处于 Established，且 VIP/32 出现在发往指定 peer 的 advertised-routes。撤销时执行 no network、write memory 并删除 loopback 地址。'],
      ['脑裂扫描', '每次都在集群所有机器执行 ip -o -4 addr show：0 个为 UNBOUND，1 个为 BOUND 或 MISMATCH，超过 1 个立即为 CONFLICT；唯一持有者不是当前主库也报错。'],
      ['零持有者与双轮证明', '绑定前必须从所有机器撤销并扫描到 0；绑定后连续两轮集群扫描都只能发现目标，任一扫描失败或不一致都会撤销新绑定并记为 FAILED。'],
      ['锁与失败补偿', '架构/VIP 操作使用 5 分钟集群锁并每分钟续租；续租丢失立即中止。原则是“宁可暂时没有 VIP，也不允许两个可能持有者”。'],
      ['数据收尾', '复制追平和 relay 回放先于提升；强制路径有显式门禁。重定向后可用 pt-table-sync 修复，pt-table-checksum 是最终强制校验。']
    ],
    ['同一集群只能有一个架构或 VIP 操作持锁', '绑定前必须获得全节点零持有者证明', '成功前必须连续两轮证明只有目标持有 VIP', '旧主未完成写入围栏不得恢复入口', '自动业务 VIP 仅接受 IPv4 L2_ARP 或 BGP', '锁续租失败必须停止破坏性步骤'],
    ['ArchitecturePlanner / Executor', 'HAService', 'Agent task channel', 'iproute2 / arping', 'FRR vtysh', 'GTID evaluator', 'pt-table-checksum / sync'],
    ['MANUAL 与 CLOUD_API 有领域枚举但当前不能自动切换。', 'BGP 要求网络侧已部署并允许 FRR peering。', '自动 VIP 仅实现 IPv4；跨三层必须提供 BGP peer。']
  ),

  'cluster-observability': detail(
    ['Agent 按动态配置采集', '心跳携带指标', 'Manager 保存快照与数值样本', '查询层选择范围和步长', '计数器转换速率并聚合', '绘制集群或实例趋势'],
    [
      ['双层数据', '约 15 秒概览快照用于快速展示，完整规范化样本用于性能 API；原始 JSON 同时保留以兼容新增采集字段。'],
      ['计数器速率化', 'QPS、网络字节等累计值用相邻样本差值除以真实间隔；进程重启、回绕或重置不会产生负值尖峰。'],
      ['标签维度', '数值叶子规范化为指标和 machine、instance、device、interface、mount 标签，可按实例过滤并做 sum、avg 或 max。'],
      ['范围与步长', '查询最长 7 天；未指定步长时目标约 120 点，允许 5–3600 秒，较长范围会下采样。'],
      ['新鲜度', '样本超过采集间隔 3 倍即标记 stale，最小容忍 30 秒，避免把停止更新的最后值当成实时健康。'],
      ['内存归因', '主机 /proc 与 performance_schema 内存事件分别采集；差额可能来自未插桩分配、allocator 碎片、线程栈或 mmap。']
    ],
    ['计数器重置不得形成负速率', '时序值必须携带采集时间与作用域', '过期数据必须可识别', '未登记实例指标不入库'],
    ['Agent collectors', 'HeartbeatService', 'Metric sample store', 'PerformanceHandler', 'Metric catalog'],
    ['性能查询和保留窗口最长 7 天。', '集群聚合可能掩盖单机热点。']
  ),

  mysql: detail(
    ['匹配版本、架构与 glibc 制品', '校验端口、server_id 和目录', '生成受控配置', 'mysqld validate-config', 'initialize-insecure', '注册 systemd', '验证后登记实例'],
    [
      ['兼容目录', '前后端共享兼容目录限制版本、CPU 架构、glibc 和工具组合；MySQL 8.x 可自动安装 Percona Toolkit，9.x 当前默认禁用。'],
      ['配置先验证', 'mysqld 必须以 --defaults-file 为首参数执行 --validate-config，通过后才允许 initialize-insecure。'],
      ['版本化目录', '软件解压到版本目录，base_dir 用符号链接指向当前版；升级用临时链接和 mv -T 原子切换。'],
      ['账号与密码', '结构化预设生成监控、备份和 MHA 账号；密码经 Agent 的 0600 短期 defaults 文件传递，不进入进程参数或日志。'],
      ['安全生命周期', '重启/关闭要求精确确认 RESTART/SHUTDOWN IP:PORT，并检查集群角色、可写状态、事务、复制和可选深度数据门禁。'],
      ['升级不可逆边界', '预检报告绑定实例、端口和制品；新版本启动可能修改数据字典，因此不能简单换回旧二进制，只能继续新版本或物理恢复。']
    ],
    ['端口和 server_id 必须唯一', '制品必须匹配版本与架构', '配置验证失败不得初始化或重启', '凭据不得进入命令行和日志', '远端成功后才更新实例状态'],
    ['Compatibility catalog', 'PackageService', 'TaskService', 'Agent MySQL installer', 'systemd', 'mysqlsh / mysqlcheck'],
    ['跨大版本升级没有旧二进制原地回滚。', '系统依赖、内核和文件系统能力仍需目标机满足。']
  ),

  'mysql-governance': detail(
    ['读取元数据与版本能力', '生成结构化预览', '执行权限与安全门禁', '提交受控 SQL 或参数任务', '重新读取目标状态'],
    [
      ['结构化输入', '界面只提交实例、库表列、变量和桶数等字段；后端安全引用标识符并生成 SQL，不向浏览器暴露任意 shell 或凭据。'],
      ['索引语义保护', '统计结合 information_schema 与 innodb_index_stats；重复和左前缀判断保护主键、唯一约束语义。'],
      ['原生在线门禁', '索引操作明确使用 ALGORITHM=INPLACE、LOCK=NONE 和 10 秒 lock_wait_timeout；不支持时失败，不静默降级为 COPY 或阻塞锁。'],
      ['PT 在线路径', '复杂变更先 precheck/dry-run，再用 pt-online-schema-change；chunk-time 0.5 秒、延迟阈值 10 秒、Threads_running 25 暂停/50 中止。'],
      ['直方图边界', '仅 MySQL 8+，桶数 1–1024；拒绝 5.7、JSON、空间列和单列唯一索引，以 NO_WRITE_TO_BINLOG 执行 UPDATE/DROP HISTOGRAM。'],
      ['实例本地统计', '直方图来自 INFORMATION_SCHEMA.COLUMN_STATISTICS 且不随复制传播，每个副本需单独维护。']
    ],
    ['不支持在线算法时必须失败', '不得删除主键或唯一约束', '直方图操作不得写 Binlog', '变更后必须重读验证'],
    ['Index service', 'Histogram service', 'Compatibility catalog', 'TaskService', 'Agent MySQL handler'],
    ['优化器是否采用直方图取决于查询与统计新鲜度。', 'INPLACE/LOCK=NONE 支持范围由表结构和 MySQL 版本决定。']
  ),

  'mysql-data-ops': detail(
    ['选择实例和对象', '只读预检或 dry-run', '绑定安全参数与指纹', '在负载门禁下执行', '持续回报进度', '验证结构/行数/健康', '保存报告与产物'],
    [
      ['在线 DDL', 'pt-online-schema-change 通过影子表、触发器和分块复制降低阻塞，但仍会产生磁盘、触发器与复制写放大。'],
      ['归档输入防护', 'WHERE 拒绝 1=1、注释、多语句、修改语句、SLEEP 和 BENCHMARK；库表标识符独立校验，预检最多扫描 10 万行。'],
      ['指纹绑定', '实例、库表、WHERE、安全参数和模式生成完整指纹；执行必须匹配成功预检，防止“检查 A、执行 B”。'],
      ['归档节流', '默认每批 1000 行、每秒一批、每批提交，锁等待或死锁最多重试 3 次；copy-only 使用 --no-delete。'],
      ['巡检深度', '标准检查可用性、连接、Binlog、持久性、GTID、容量；深度再查无主键、非 InnoDB、碎片、长事务、临时盘、buffer 命中与 undo。'],
      ['评分与报告', '100 分起，warning 扣 8、critical 扣 20；结果可汇总并导出 HTML/JSON、DOCX 或 XLSX。'],
      ['升级门禁', '报告 30 分钟有效并绑定实例、端口与制品；检查链接、版本、配置、磁盘、事务、复制，优先 mysqlsh checker，回退 mysqlcheck。']
    ],
    ['预检指纹不一致不得归档', '在线变更不得静默转为阻塞算法', '删除源数据必须显式选择', '升级目标变化须重新预检', '任务凭据只存在 Agent 短期文件'],
    ['pt-online-schema-change', 'pt-archiver', 'Inspection handler', 'Upgrade precheck', 'Agent defaults-file helper'],
    ['在线工具仍会产生磁盘、网络和复制压力。', '巡检是规则化风险发现，不替代业务一致性验证。']
  ),

  'binlog-analysis': detail(
    ['验证实例、范围和 MHA 凭据', '进入全局并发队列', '用复制协议定位 Binlog', '解析 Rotate/GTID/Query/ROW/XID', '按事务、表和时间桶聚合', '应用明细上限', '返回结果或响应取消'],
    [
      ['只读复制协议', '使用 go-mysql 作为 Binlog dump 客户端，不执行写 SQL；账号来自已启用 MHA 预设，HTTP 模型不携带明文凭据。'],
      ['事件还原', '解析 TABLE_MAP、WRITE/UPDATE/DELETE_ROWS、QUERY、GTID、ROTATE 与 XID，按事务边界计算行数、字节和文件位置。'],
      ['热点与延迟', 'ROW 事件按库表和时间桶统计 insert/update/delete；GTID original/current commit timestamp 用于还原历史复制延迟趋势。'],
      ['大事务', '可按影响行数或事件字节设阈值，保留 GTID、位置、表分布和时间；DDL 作为独立列表保存。'],
      ['受控并发', 'Manager 全局最多 2 个分析任务；独立文件可自适应并行，跟随 Rotate 的连续流保持顺序，取消会传播到解析器。'],
      ['内存保护', 'DML 明细最多 20000、DDL 5000、大事务 5000；达到上限仍返回聚合，并以 truncation 标志说明不完整。'],
      ['脱敏', '连接与解析错误先清除敏感内容；任务列表仅返回请求元数据、状态和摘要。']
    ],
    ['不得对源库执行写操作', '凭据不得进入 API 和错误文本', '跨文件事务边界必须正确', '明细截断必须显式标记', '取消后必须释放并发槽'],
    ['BinlogAnalysisService', 'go-mysql replication', 'Parser / aggregator', 'MHA account presets', 'In-memory jobs'],
    ['单次范围最长 7 天。', '任务和结果当前只在内存，Manager 重启会丢失。', 'statement/mixed 模式不能像 ROW 一样还原全部行级影响。']
  ),

  'sql-diagnostics': detail(
    ['采集当前或历史语句', '规范化摘要并标记覆盖度', '按延迟/次数/行数聚合', 'EXPLAIN 访问路径', 'Kill 前重核连接身份', '执行 KILL QUERY 并审计'],
    [
      ['多数据源', '当前 SQL 组合 processlist 与 performance_schema current；历史来自 history_long；慢 SQL 合并已完成、运行中和 TABLE 模式 mysql.slow_log。'],
      ['Top SQL 差分', 'digest summary 是累计值，服务以相邻快照差分计算区间指标，并识别 MySQL 重启或 performance_schema 重置。'],
      ['覆盖度', '采集源缺失、窗口过短或消费者未启用时返回 warnings/coverage；数据空洞不能伪装成“没有慢 SQL”。'],
      ['受限诊断', '服务限制只读语句和返回行数，摘要可掩码字面量，单条 SQL 文本最多 64 KiB。'],
      ['连接复用防护', 'Kill 前比较 connection_id、digest 和开始时间；MySQL 连接 ID 已复用时返回 409，避免误杀新会话。'],
      ['最小终止', '只执行 KILL QUERY；系统线程/账号被保护，要求精确 KILL id、至少 3 字原因和操作者，成功失败都审计。']
    ],
    ['采集缺口必须暴露', '连接身份变化不得继续 Kill', '系统线程和保护账号不得终止', 'Kill 必须有原因与审计', '不得修改目标慢日志配置'],
    ['SQLDiagnosticService', 'performance_schema', 'processlist / slow_log', 'Digest snapshots', 'Kill audit store'],
    ['history_long 容量与 slow_log 配置由实例决定。', 'KILL QUERY 不会自动修复锁争用根因。']
  ),

  flamegraph: detail(
    ['选择 system/PID/process', '校验时长和频率', 'Agent 探测后端', '本机采样生成 folded stacks', 'Manager 保存结果', '浏览器渲染、搜索、导出'],
    [
      ['本机执行', '采样在目标主机运行，Manager 仅计划、持久化和展示，不把 /proc 或 perf 直接暴露给浏览器。'],
      ['后端降级', '优先 perf；pid/process 目标缺少 perf 或权限时可回退 /proc/<pid>/stack，并记录实际后端。'],
      ['折叠格式', '调用栈统一为 folded stacks，持久化样本数与后端，前端据此下钻宽栈和搜索函数。'],
      ['计划调度', '支持 once、interval、daily；调度器约每 10 秒发现到期计划并创建标准任务。'],
      ['内存双视角', '主机 RSS 与 performance_schema memory_summary_global_by_event_name 分开采集，差额提示碎片、线程栈或 mmap。']
    ],
    ['采样时长 1–600 秒', '必须保存实际后端和样本数', '计划运行必须有任务', '读取结果不得再次采样'],
    ['FlameGraphService', 'Agent profiler', 'perf / procfs', 'Schedule runner', 'Browser renderer'],
    ['内核符号、debug symbol 和 perf 权限影响栈质量。', '/proc 回退不等价于完整统计 CPU 火焰图。']
  ),

  automation: detail(
    ['解析集群和目标', '创建父任务', '按机器/实例展开子任务', '在并发边界内分发', '等待全部终态', '汇总报告和产物'],
    [
      ['父子编排', '一次批量操作对应一个业务父任务，每个目标有独立子任务、步骤、事件和结果，局部失败不会丢掉其他节点证据。'],
      ['目标快照', '提交时解析成员和实例，报告以当次 task_ids 汇总；后续成员变化不会改写已执行批次。'],
      ['类型化操作', '资产采集和巡检使用受控 handler 与结构化结果；自定义脚本是显式高权限能力，不与内置操作混用。'],
      ['终态屏障', '所有子任务进入 success/failed 等终态后结果接口才把 ready 设为 true，避免把部分数据当完整报告。'],
      ['产物隔离', 'HTML、CSV、JSON 和文件都通过 task_id 与登记文件名下载，不开放 Agent 任意文件系统路径。']
    ],
    ['每个远端目标必须有独立子任务', '报告必须显示 pending/failed 数', '范围以提交时快照为准', '只能下载登记产物'],
    ['TaskHandler', 'TaskService', 'Agent dispatcher', 'Result aggregator', 'Artifact endpoint'],
    ['自定义脚本以 Agent 服务账号权限运行，平台无法理解其内部副作用。', '大批量任务受在线率和网络带宽限制。']
  ),

  backup: detail(
    ['调度器发现到期策略', '选择全量或增量链', 'Agent 获取实例 flock', '检查磁盘和复制延迟', '执行并写完成标志', '保存运行记录', '恢复时重建链并事务性替换目录'],
    [
      ['调度', '每约 30 秒检查 once、custom、weekly；同一周可按工作日选全量/增量，失败重试 0–5 次，目标路径必须为绝对路径。'],
      ['增量链', '增量引用同实例最近成功备份；恢复沿 base_id 最多追溯 100 层，链尾必须是同实例全量。'],
      ['互斥与门禁', '每端口用 flock 防并发；每次尝试前检查磁盘阈值，并等待副本延迟在最多 30 秒内归零。'],
      ['凭据和标志', 'Agent 创建 0600 临时 defaults 文件并 trap 删除；成功目录必须有 .gmha-backup-complete、版本和位点元数据。'],
      ['工具匹配', 'MySQL 5.7 使用 XtraBackup 2.4，8/9 按精确 x.y 系列匹配，避免生成不兼容备份。'],
      ['物理恢复', '增量依次 apply-log-only、最终 prepare；data、binlog、redo、undo 等目录先移走，失败时回滚并重启原实例。'],
      ['PITR 与闪回', 'PITR 从 xtrabackup_binlog_info 以 mysqlbinlog 回放至 stop time；闪回由 bin2sql 生成反向 SQL，可预览后显式应用。']
    ],
    ['无成功全量根不得恢复增量', '无完成标志不得记成功', '同端口不得并发备份', '恢复须精确确认 RESTORE runID', '目录替换失败须恢复原目录'],
    ['BackupService', 'Agent backup script', 'XtraBackup', 'flock / marker', 'mysqlbinlog', 'bin2sql'],
    ['备份成功不等于应用级可恢复，必须演练。', '异地复制、保留和加密由外部存储策略负责。']
  ),

  alerts: detail(
    ['心跳进入评估队列', '规则累计连续次数', 'fingerprint 合并事件', '转为 firing/resolved', '写持久 outbox', '通知投递重试', '人工处置或自动化 CAS'],
    [
      ['Manager 评估', 'Agent 只采集；规则、严重级别和事件状态都由 Manager 计算，同一心跳样本只评估一次。'],
      ['抗抖与合并', 'consecutive_count 抑制瞬时抖动，资源标签形成稳定 fingerprint；重复触发合并，严重级别可升级并自动恢复。'],
      ['通知节流', 'repeat_interval_seconds 和 max_notifications 控制重复通知；notice、warning、critical、fatal 都保留投递记录。'],
      ['有界队列', '评估与通知队列有容量；溢出时按 Agent 合并最新样本，避免高压下无限占用内存。'],
      ['持久 outbox', '通知先持久化再异步发送；队列满或重启后可重放，投递失败不会丢失事件事实。'],
      ['自动化 CAS', 'pending→claimed→running→succeeded/failed/skipped 用 expected_state 比较交换；冲突返回 409，不自动执行未配置的破坏性动作。'],
      ['导出与脱敏', 'Prometheus/Zabbix 导出状态；渠道密码、令牌、密钥和 URL 在读取时掩码。']
    ],
    ['同一样本不得重复评估', '通知失败不得丢事件', '自动化只能按期望状态推进', '静默不改变 firing 事实', '渠道秘密不得回显'],
    ['AlertService', 'Rule evaluator', 'Fingerprint store', 'Durable outbox', 'Notification workers', 'Exporters'],
    ['规则正确性依赖指标新鲜度与标签范围。', '确认和静默不修复底层故障。']
  ),

  'ai-automation': detail(
    ['读取活动告警与机器摘要', '构造脱敏运维上下文和系统约束', '调用选定模型', '解析 answer/findings/plans', '过滤非白名单或无目标计划', '按固定目录赋予风险与 30 分钟有效期', '审批或精确确认', '创建标准任务并保存审计'],
    [
      ['模型接入与密钥', '支持 OpenAI 兼容 chat/completions 与 Anthropic messages；远程地址必须 HTTPS，仅 localhost/loopback 可用 HTTP。API key 用本机 32 字节密钥和 AES-256-GCM 加密，返回时只显示掩码。'],
      ['最小上下文', '模型上下文只包含告警摘要、最多 50 条 firing 事件及机器 ID、名称、IP、集群、状态和截断错误，不把 SSH/MySQL 密码发送给模型。HTTP 调用超时 45 秒，响应最多读取 2 MiB。'],
      ['服务端动作与 API 目录', 'GET /ai/capabilities 返回固定 actions 和完整 cluster_endpoints。集群登记、成员、VIP、架构、立即备份、滚动升级、批量卸载与清理都映射到真实应用服务；未知动作或空 target_id 被丢弃，每次最多接受 5 个计划，模型不能生成任意 shell。'],
      ['风险不可由模型决定', '风险来自 Manager 固定目录：只读诊断/VIP 复检为 low，元数据与立即备份为 medium，VIP/架构为 high，卸载、滚动升级和清理为 critical；模型给出的风险字段不会覆盖服务端分类。'],
      ['密钥输入隔离', '集群安装、备份策略、恢复和账号等 API 在 cluster_endpoints 中标为 secure_input_api，并列出 sensitive_parameters。AI 说明准确入口，但密码只能通过受保护表单或密钥通道提交，不能进入聊天或计划。'],
      ['VIP 意图兜底', '用户明确要求添加、绑定、漂移或撤销 VIP 时，即使模型没有返回计划，Manager 也会确定性映射到 VIP 白名单动作；缺少地址、网段、目标机器或网卡时生成 blocked 计划并列明参数，绝不会猜测生产地址或误报没有 API。'],
      ['分级审批', '计划有效期 30 分钟。中风险按策略要求 approved=true；高/极高风险始终要求与服务端生成的“确认动作 目标”短语精确匹配，always_confirm_high_risk 无法关闭。'],
      ['授权再校验', '执行时重新检查计划仍为 proposed/approval_required、未过期、动作在 allowed_actions 中且审批满足；并发或重复执行返回冲突。'],
      ['落地为任务', 'AI 不直接执行任意命令。批准后映射到 TaskService、HAService、BackupService、ClusterUpgradeService 或 MachineService；批量操作返回父 task_id，计划状态只表示 submitted，最终结果还必须通过任务和实机后置条件。'],
      ['定时分析', '自动分析间隔限制为 5–1440 分钟；启用且配置默认模型后按策略触发，运行、发现、计划、执行时间和错误持久化用于审计。']
    ],
    ['模型不得执行任意命令', '动作风险由服务端固定目录决定', '高/极高风险确认不可关闭', '过期或已处理计划不得重复执行', '模型上下文和响应不得包含保存的 API key', '任务提交与任务成功必须分别判断'],
    ['AIService', 'AES-GCM secret store', 'AlertService / MachineService', 'Model HTTP client', 'Plan repository', 'TaskService whitelist'],
    ['需要密码的操作必须在对应安全表单完成输入，不能作为普通 AI 参数执行。', 'restart_mysql 当前映射固定 systemctl restart mysqld，执行前仍需人工确认具体实例与集群影响。', '故障切换和强制继续仍受独立围栏与人工决策约束。', '外部模型的数据处理和留存受所选供应商政策约束。']
  ),

  packages: detail(
    ['下载或上传制品', '分类保存元数据', '校验 SHA256', '匹配组件和架构', '创建升级任务', '备份当前程序', '原子替换并健康检查', '失败恢复备份'],
    [
      ['唯一制品来源', 'MySQL、工具、Agent 与 Manager 安装引用登记制品，任务不从任意 URL 或未校验路径拉取软件。'],
      ['完整性与兼容性', '文件名、分类、版本、CPU 架构、glibc 与 SHA256 一起校验；组件升级拒绝相同版本和不支持的降级。'],
      ['Agent 灰度', '批量升级按目标创建任务，可先用少量节点验证能力、心跳和任务通道再扩面。'],
      ['Manager 原子替换', '候选先自检，备份当前可执行文件后原子替换，通过启动和健康端点验证；失败使用备份恢复。'],
      ['源码重建', 'rebuild 只接受服务器本地源码目录与精确 REBUILD 确认，执行 Go 编译、候选自检、安装、重启并留下记录。']
    ],
    ['未校验制品不得执行', '已验证文件不得原地修改', '候选自检失败不得替换', '升级须保存当前与目标版本'],
    ['PackageService', 'Compatibility catalog', 'UpgradeService', 'Agent upgrade handler', 'Manager rebuild'],
    ['制品库不是跨站点对象存储。', 'Manager 自升级会短暂中断当前 HTTP 连接。']
  ),

  tasks: detail(
    ['持久化 pending', '在线通道发送为 sent', 'Agent 回报 running 和步骤', '事件持续追加', '写入 success/failed', '父任务聚合子任务'],
    [
      ['单向状态机', 'pending、sent、running 走向 success/failed；远端结果不靠页面猜测，步骤和事件记录当前位置。'],
      ['先持久化后分发', '有副作用的操作先取得 task_id，再向 Agent 发送；客户端断开仍能按 ID 查询。'],
      ['父子关系', '批量、备份和架构运行用父任务表达意图，子任务对应机器或阶段，可精确表达部分成功。'],
      ['结构化事件', 'Reporter 串行回传进度、INFO/WARN/ERROR 和结果，Manager 持久化原始证据。'],
      ['清理边界', '删除只移除已完成任务的 Manager 历史，不撤销远端变化；运行中任务禁止删除。']
    ],
    ['远端副作用必须有任务或运行记录', '终态不可回退', '运行中不能删除', '父任务不得掩盖失败子任务', '日志不得含秘密'],
    ['TaskService', 'Task / Step / Event repositories', 'Agent WebSocket', 'Reporter', 'Parent-child aggregator'],
    ['不是所有命令都可安全取消，中断后需按步骤检查实际状态。', 'Manager 重启期间远端任务要靠回报或协调收敛。']
  ),

  manager: detail(
    ['读取真实进程和配置', '测试目标数据库', '用短期 token 保存', '启动替代进程并等待监听', '延迟终止旧进程', '恢复调度、心跳和任务服务'],
    [
      ['当前进程接管', 'RuntimeService 登记正在提供 Web 的进程，状态以实际服务为证据；陈旧 PID 文件不能让已停止进程显示 running。'],
      ['数据库门禁', '数据库变化须先测试并取得十分钟 test_token，保存时再次校验 token 与配置绑定；密码和完整 DSN 不回显。'],
      ['进程生命周期', '后台启动写日志；停止先 SIGTERM，超时才 SIGKILL；当前 Web 进程要保护响应交付与监听切换。'],
      ['平滑替代', '重启先启动替代进程并等待 HTTP/gRPC 监听，再延迟结束旧进程，便于 API 返回和 Agent 重连。'],
      ['升级恢复', '升级阶段持久化；重启后协调未完成 job，核对版本、健康端点和监听，而非只信替换命令退出码。'],
      ['控制面职责', '同一进程承载 REST、心跳、任务、HA、告警、备份和调度，数据库或监听错误会同时影响这些能力。']
    ],
    ['数据库变更须有未过期测试 token', '状态必须核对真实进程', '密码与 DSN 不得回显', '替代进程未就绪不得结束可用进程'],
    ['ManagerRuntimeService', 'HTTP / gRPC listeners', 'Database tester', 'Process supervisor', 'Upgrade reconciler'],
    ['SQLite 只适合单节点，多 Manager 必须共享 MySQL/PostgreSQL。', '关闭唯一 Manager 会中断控制面。']
  ),

  'manager-ha': detail(
    ['验证共享数据库', '保存 HA 与 L2 VIP 配置', '节点每 5 秒登记状态', '生成 15 分钟引导令牌', 'Agent 下载同版本程序和配置', '安装 systemd 并探活', '确认备用健康', '源节点删除 VIP', '目标节点 replace VIP 并 arping -A', '更新 active 角色'],
    [
      ['共享状态', 'SQLite 只允许单节点；MySQL/PostgreSQL 让多个 Manager 共享控制面状态，第一个登记节点成为 active，其余作为 standby。'],
      ['租约与探活', '本地约每 5 秒保存 heartbeat/state；概览用 1.5 秒 HTTP 客户端探测远端 /api/v1/manager/status。ready 要求共享库、至少两节点、VIP 与 active 同时成立。'],
      ['受控引导', '节点必须来自已纳管机器。随机 bootstrap grant 有效 15 分钟，Agent 经 no-store 端点下载当前运行版二进制与数据库配置。'],
      ['安装权限', '二进制 0755、配置 0600，systemd 管理服务并最多约 30 秒循环探活；节点 start/restart/stop 均走 Agent 任务。'],
      ['当前 VIP 切换', '先在源节点 ip addr del 并等待最多约 60 秒成功，再在目标 ip addr replace；启用时发送 arping -A -c 3，最后更新 active_node_id。'],
      ['与业务 VIP 的差异', 'Manager VIP 仅 L2 迁移：没有 FRR/vtysh BGP、没有全节点持有者扫描、没有零持有者集群证明，也没有连续两轮唯一持有者验证。']
    ],
    ['必须使用共享 MySQL/PostgreSQL', '备用节点必须已纳管', 'bootstrap token 必须短期且禁止缓存', '目标健康前不得作为入口', '源节点删除失败不得绑定目标'],
    ['ManagerHAService', 'Shared repository', 'Agent systemd installer', 'Bootstrap endpoints', 'iproute2 / arping'],
    ['当前没有自动选主共识协议。', 'Manager VIP 不支持 BGP 或业务 VIP 级防脑裂。', '旧节点失联时需外部主机/网络围栏。']
  )
}
