# Keepalived安装部署

------

## 目录



## 安装步骤

假设存在两个节点 172.25.108.41，172.25.108.42 VIP是172.25.108.43

1. 解压并配置

   ```bash
   tar zxvf keepalived-2.2.7.tar.gz 
   configure options: --prefix=/usr/local/keepalived
   
   Config options:  LVS VRRP VRRP_AUTH OLD_CHKSUM_COMPAT FIB_ROUTING
   
   System options:  PIPE2 SIGNALFD INOTIFY_INIT1 VSYSLOG EPOLL_CREATE1 IPV6_ADVANCED_API RTA_ENCAP RTA_EXPIRES FRA_TUN_ID RTAX_CC_ALGO RTAX_QUICKACK FRA_OIFNAME IFA_FLAGS IP_MULTICAST_ALL NET_LINUX_IF_H_COLLISION LIBIPTC_LINUX_NET_IF_H_COLLISION VRRP_VMAC IFLA_LINK_NETNSID CN_PROC SOCK_NONBLOCK SOCK_CLOEXEC O_PATH GLOB_BRACE INET6_ADDR_GEN_MODE SO_MARK SCHED_RT SCHED_RESET_ON_FORK
   ```

2. 创建软连接

   ```bash
   ln -s /usr/local/keepalived/sbin/keepalived /usr/local/keepalived/bin/keepalived
   ```

3.  配置环境变量

   ```bash
   cat >> /etc/profile <<'EOF'
   PATH=$PATH:$HOME/bin:/usr/local/mysql/bin/:/usr/local/keepalived/bin/
   export PATH
   EOF
   source /etc/profile
   ```

4. 编辑Keepalived参数文件

   说明：

   1、 虚拟ip为：172.25.108.43。

   2、 通过监控3306端口，来实现切换。

   3、 节点1的权重为100，节点2的权重为90，节点1的权重高于节点2。

   4、 每个节点中数据库virtual_router_id为80。

   

   节点一配置文件

   ```bash
   #vi /etc/keepalived/keepalived.conf 
   global_defs {
      router_id MySQL-HA
   }
   
   vrrp_instance VI_1 {
       state BACKUP 
       interface eth0
       virtual_router_id 43 --避免与其他高可用配置冲突，virtual_router_id置为VIP的最后一位(注释需删除)
       priority 100
       advert_int 2
       nopreempt
       authentication {
           auth_type PASS
           auth_pass 1111
       }
       virtual_ipaddress {
           172.25.108.43    }
   }
   
   virtual_server 172.25.108.43 3306 {
       delay_loop 2
       lb_algo wlc
       lb_kind DR
       persistence_timeout 60
       nat_mask 255.255.255.0
       protocol TCP
   
       real_server 172.25.108.41 3306 {
           weight 1
           notify_down /etc/keepalived/mysql-down.sh
             
           TCP_CHECK {
               connect_timeout 3
               retry 2
               delay_before_retry 1
               connect_port 3306
           }
       }
   }
   
   ```

   节点二配置文件

   ```bash
   节点2上编辑配置文件: 
   vi /etc/keepalived/keepalived.conf 
   global_defs {
      router_id MySQL-HA
   }
   
   vrrp_instance VI_1 {
       state BACKUP 
       interface eth0
   virtual_router_id 43 --避免与其他高可用配置冲突，virtual_router_id置为VIP的最后一位(注释需删除)
       priority 90
       advert_int 2
       nopreempt
       authentication {
           auth_type PASS
           auth_pass 1111
       }
       virtual_ipaddress {
           172.25.108.43
       }
   }
   
   virtual_server 172.25.108.43 3306 {
       delay_loop 2
       lb_algo wlc
       lb_kind DR
       persistence_timeout 60
       nat_mask 255.255.255.0
       protocol TCP
   
       real_server 10.184.128.24 3306 {
           weight 1
           notify_down /etc/keepalived/mysql-down.sh
             
           TCP_CHECK {
               connect_timeout 3
               retry 2
               delay_before_retry 1
               connect_port 3306
           }
       }
   }
   ```

5. **设置服务脚本**

   ```bash
   vi /etc/keepalived/mysql-down.sh
   
   #!/bin/bash
   echo "root" | sudo -S su - ;/usr/bin/pkill keepalived
   
   #chmod 750 /etc/keepalived/mysql-down.sh
   #chmod +x /etc/keepalived/mysql-down.sh
   ```

6. 配置systemctl

   ```bash
   cat > /etc/systemd/system/keepalived.service <<'EOF'
   [Unit]
   Description=Keepalived High Availability Service
   After=network-online.target
   Wants=network-online.target
   
   [Service]
   Type=forking
   ExecStart=/usr/local/keepalived/bin/keepalived -f /etc/keepalived/keepalived.conf
   ExecReload=/bin/kill -HUP $MAINPID
   KillMode=process
   Restart=on-failure
   RestartSec=5
   
   [Install]
   WantedBy=multi-user.target
   EOF
   
   systemctl daemon-reload
   systemctl enable keepalived
   ```

   

   

   
