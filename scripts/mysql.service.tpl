[Unit]
Description=MySQL Server
Documentation=man:mysqld(8)
After=network.target
After=syslog.target

[Install]
WantedBy=multi-user.target

[Service]
User=__MYSQL_USER__
Group=__MYSQL_USER__
ExecStart=__BASE_DIR__/bin/mysqld --defaults-file=/etc/my.cnf
LimitNOFILE=5000
LimitNPROC=10000
