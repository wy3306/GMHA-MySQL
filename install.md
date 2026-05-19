# 安装部署

------

### 1.1 系统初始优化

1. 关闭防火墙

   ```bash
   systemctl stop firewalld && systemctl disable firewalld
   ```

2. 关闭SELinux

   ```bash
   setenforce 0
   sed -i '7s#SELINUX=enforcing#SELINUX=disabled#g' /etc/selinux/config 
   ```

3. 检查yum仓库配置

   ```bash
   yum list
   ```

4. 清理可能存在的其他数据库残留配置

   ```bash
   rpm -qa|grep mariadb
   mariadb-libs-5.5.68-1.el7.x86_64
   yum remove -y `rpm -qa|grep mariadb`
   ```

5. 安装常用软件以及MySQL依赖 libaio-devel 

   ```bash
   yum install -y libaio-devel
   ```

### 1.2 操作系统内核优化

1. 修改登录限制和unlimit参数

   ```bash
   cat >> /etc/pam.d/login <<EOF
   session    optional     pam_limits.so
   session    required     pam_limits.so
   EOF
   
   ulimit -n 65536  
   ulimit -u 65536  
   ulimit -l unlimited
   
   sed -i s/*/#*/ /etc/security/limits.conf
   cat >> /etc/security/limits.conf <<EOF
   *                soft    nofile          65536      
   *                hard    nofile          65536 
   *                soft    nproc           65536      
   *                hard    nproc           65536 
   *                soft    memlock         unlimited  
   *                hard    memlock         unlimited
   EOF
   
   sed -i s/*/#*/ /etc/security/limits.d/90-nproc.conf 
   sed -i s/root/#root/ /etc/security/limits.d/90-nproc.conf 
   cat >> /etc/security/limits.d/90-nproc.conf  <<EOF
   *          soft    nproc     65536
   root       soft    nproc     unlimited
   EOF
   ```

2. 修改内核参数

   ```bash
   cat >> /etc/sysctl.conf  <<EOF
   
   #shmmax=memory*1024*1024*1024*90%
   #shmall=shmmax/getconf PAGESIZE
   #kernel.shmmax = 7730941132                       
   #kernel.shmall = 1887436                          
   #kernel.shmmni = 4096                              
   kernel.sem = 1250 320000 100 256                 
   net.ipv4.tcp_syncookies = 1                       
   net.ipv4.tcp_tw_reuse = 1                         
   net.ipv4.tcp_tw_recycle = 1                       
   net.ipv4.tcp_keepalive_time = 300                 
   net.ipv4.tcp_keepalive_intvl = 30                 
   net.ipv4.tcp_keepalive_probes = 3                 
   net.ipv4.tcp_fin_timeout = 30                     
   net.ipv4.tcp_max_syn_backlog = 4096
   net.ipv4.tcp_timestamps = 1
   net.ipv4.tcp_syn_retries = 2
   net.ipv4.tcp_synack_retries = 2
   net.ipv4.tcp_mem = 94500000 915000000 927000000
   net.ipv4.tcp_max_orphans = 262144
   net.ipv4.ip_local_port_range = 9000 65500         
   net.core.somaxconn = 65536                       
   net.core.netdev_max_backlog = 65536              
   net.core.rmem_default = 262144                    
   net.core.rmem_max = 4194304                       
   net.core.wmem_default = 8388608                    
   net.core.wmem_max = 20971520                       
   fs.file-max = 6815744                             
   fs.aio-max-nr = 4194304
   vm.min_free_kbytes = 51200
   vm.dirty_background_ratio = 10                    
   vm.dirty_ratio = 20                               
   vm.zone_reclaim_mode = 0                          
   net.ipv4.ip_local_port_ range = 20000 65535
   vm.swappiness = 1
   EOF
   
   sysctl -p
   
   echo 0 > /proc/sys/vm/zone_reclaim_mode
   ```

3. 修改IO调度算法

   ```bash
   echo deadline > /sys/block/sda/queue/scheduler
   echo "echo deadline > /sys/block/sda/queue/scheduler" >> /etc/rc.local
   
   echo deadline > /sys/block/sdb/queue/scheduler
   echo "echo deadline > /sys/block/sdb/queue/scheduler" >> /etc/rc.local
   
   echo deadline > /sys/block/sdc/queue/scheduler
   echo "echo deadline > /sys/block/sdc/queue/scheduler" >> /etc/rc.local
   ```

### 1.3 下载安装包（二进制）

1. 查看系统 glibc 版本

   ```bash
   rpm -aq |grep glibc
   ```

2. 下载安装包（系统的glibc版本需要大于>二进制安装包的glibc版本，这里以在x86机器glibc2.12环境下安装mysql）

   - 前往 https://downloads.mysql.com/archives/community/ 下载
   - wget -p /opt https://downloads.mysql.com/archives/get/p/23/file/mysql-8.0.36-linux-glibc2.12-x86_64.tar

### 1.4 安装步骤

1. 创建用户（可自定义，单后续的my.cnf，mysql.service，以及初始化中也要跟随修改用户）

   ```bash
   useradd mysql -M -s /sbin/nologin
   ```

2. 创建数据目录

   ```bash
   mkdir -p /data/3306/binlog
   mkdir -p /data/3306/data
   mkdir -p /data/3306/redo
   mkdir -p /data/3306/undo
   mkdir -p /data/3306/tmp
   ```

3. 解压安装包

   ```bash
   tar -xvf /opt/mysql-8.0.36-linux-glibc2.12-x86_64.tar -C /usr/local
   # 如果解压后还有安装包
   cd /usr/local && tar -xvf mysql-8.0.36-linux-glibc2.12-x86_64.tar.xz
   ```

4. 创建软连接

   ```bash
   cd /usr/local && ln -s mysql-8.0.36-linux-glibc2.12-x86_64 mysql
   ```

5. 设置环境变量

   ```bash
   echo 'export PATH="$PATH:/usr/local/mysql/bin"' >> /etc/profile
   source /etc/profile
   ```

6. 检查是否安装成功

   ```bash
   mysql -V
   ```

7. 编写 my.cnf

   ```bash
   cat > /etc/my.cnf << EOF
   [mysql]
   socket=/data/3306/data/mysql.sock
   
   [mysqld]
   # =========================
   # 基础参数
   # =========================
   server_id = 1
   basedir = /usr/local/mysql
   datadir = /data/3306/data
   character_sets_dir = /usr/local/mysql/share/charsets
   plugin_dir = /usr/local/mysql/lib/plugin
   port = 3306
   socket = /data/3306/data/mysql.sock
   pid_file = /data/3306/data/mysqld.pid
   tmpdir = /data/3306/tmp
   
   character_set_server = utf8mb4           # 修改：原 utf8 改为 utf8mb4，避免 utf8mb3 问题
   collation_server = utf8mb4_0900_ai_ci    # 新增：与 utf8mb4 配套
   autocommit = 1
   transaction_isolation = READ-COMMITTED
   lower_case_table_names = 1               # 保留：如已在线上使用且整套环境一致，可继续；新环境请谨慎
   auto_increment_offset = 1
   auto_increment_increment = 2
   
   # =========================
   # 连接参数
   # =========================
   interactive_timeout = 1800
   wait_timeout = 1800
   lock_wait_timeout = 1800
   skip_name_resolve = 1
   max_connections = 1000                   # 修改：原 8000 过大，先收敛，避免线程内存放大
   max_connect_errors = 1000
   max_allowed_packet = 64M                 # 修改：原 8M 偏小，适当调大
   
   # =========================
   # 日志参数
   # =========================
   log_error = /data/3306/data/mysqld.log            # 修改：使用绝对路径，避免相对路径歧义
   log_timestamps = SYSTEM
   
   slow_query_log = 1
   slow_query_log_file = /data/3306/data/slow.log    # 修改：使用绝对路径
   long_query_time = 2
   min_examined_row_limit = 100
   log_slow_admin_statements = 1
   log_slow_replica_statements = 1                        # 修改：原 log_slow_slave_statements 改为新名称
   # log_queries_not_using_indexes = 1                    # 建议默认关闭，排查时再开启，避免日志噪音
   log_throttle_queries_not_using_indexes = 10
   
   # =========================
   # 二进制日志 / 复制参数（MHA 关键）
   # =========================
   log_bin = /data/3306/binlog/mysql-bin            # 新增：MHA/复制必须启用 binlog
   binlog_format = ROW                                    # 新增：复制建议使用 ROW
   sync_binlog = 1                                        # 新增：与 innodb_flush_log_at_trx_commit=1 搭配
   binlog_expire_logs_seconds = 604800                    # 修改：替代 expire_logs_days=7，604800 秒=7天
   binlog_rows_query_log_events = 1
   log_replica_updates = 1                                # 修改：原 log_slave_updates 改为新名称
   
   gtid_mode = ON                                         # 新增：建议启用 GTID
   enforce_gtid_consistency = ON                          # 新增：GTID 配套参数
   
   # =========================
   # InnoDB 参数
   # =========================
   default_storage_engine = InnoDB
   innodb_data_file_path = ibdata1:1024M:autoextend
   innodb_temp_data_file_path = ibtmp1:512M:autoextend:max:30720M
   
   # innodb_undo_tablespaces = 4                          # 删除：8.0.14 起不再可配置，不要再写
   innodb_undo_directory = /data/3306/undo/3306
   innodb_log_group_home_dir = /data/3306/redo/3306
   innodb_redo_log_capacity = 5G                          # 修改：替代 innodb_log_file_size * innodb_log_files_in_group
   # innodb_log_file_size = 1G                            # 删除：8.0.30 起已废弃
   # innodb_log_files_in_group = 5                        # 删除：8.0.30 起已废弃
   
   innodb_flush_log_at_trx_commit = 1
   innodb_lock_wait_timeout = 600
   innodb_file_per_table = 1
   innodb_flush_method = O_DIRECT
   innodb_buffer_pool_size = 18G                          # 保留：按实际内存调整
   innodb_buffer_pool_instances = 8
   innodb_log_buffer_size = 16M
   
   innodb_read_io_threads = 8                             # 修改：原 24 偏高，先用更稳妥值
   innodb_write_io_threads = 8                            # 修改：原 24 偏高，先用更稳妥值
   
   # =========================
   # 缓冲区参数
   # =========================
   key_buffer_size = 32M                                  # 修改：InnoDB 为主时无需太大
   table_open_cache = 4096
   thread_cache_size = 128                                # 修改：原 32 偏小，可适当提高
   
   sort_buffer_size = 2M                                  # 修改：原 8M 偏大，属于 per-session 内存
   read_buffer_size = 1M                                  # 修改：原 8M 偏大，属于 per-session 内存
   read_rnd_buffer_size = 4M                              # 修改：原 32M 偏大，属于 per-session 内存
   join_buffer_size = 1M                                  # 修改：原 2M 可再保守一点
   myisam_sort_buffer_size = 64M
   
   # =========================
   # 复制节点建议项（按角色启用）
   # =========================
   # 以下参数建议只在从库/副本上启用
   # relay_log = /data/mysql/relaylog/3306/relay-bin      # 新增：从库建议配置 relay log 路径
   # relay_log_recovery = 1                               # 新增：从库重启时自动恢复 relay log
   # read_only = 1                                        # 新增：从库建议开启
   # super_read_only = 1                                  # 新增：从库建议开启，防止高权限误写
   # skip_replica_start = 1                               # 可选：如需人工控制复制启动则开启
   
   # =========================
   # 其他
   # =========================
   # sql_mode 可按业务单独定制，如有老业务兼容问题再单独评估
   
   EOF
   ```

8. 编写mysql.service

   ```bash
   cat << EOF > /usr/lib/systemd/system/mysql.service
   [Unit]
   Description=MySQL Server
   Documentation=man:mysqld(8)
   Documentation=http://dev.mysql.com/doc/refman/en/using-systemd.html
   After=network.target
   After=syslog.target
   
   [Install]
   WantedBy=multi-user.target
   
   [Service]
   User=mysql
   Group=mysql
   ExecStart=/usr/local/mysql/bin/mysqld --defaults-file=/etc/my.cnf
   LimitNOFILE=5000
   LimitNPROC=10000
   EOF
   
   # 重载配置文件并设置mysql开机自启
   systemctl daemon-reload && systemctl enable mysql
   ```

9. 初始化mysql

   ```bash
   mysqld --initialize-insecure  --user=mysql  --datadir=/data/3306/data/  --basedir=/usr/local/mysql
   ```
   
10. 登陆MySQL

    ```bash
    mysql -uroot
    ```

11. 重新设置root密码

    ```dbash
    alter user root@'localhost' identified by '123456'
    ```
