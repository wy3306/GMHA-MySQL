#!/usr/bin/env bash
set -euo pipefail

mysql_image="${MYSQL_IMAGE:-mysql:8.4}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
router_stage="$(mktemp -d)"
prefix="gmha-mgr-accept-${$}"
network="${prefix}-net"
root_password="gmha-root-${$}"
admin_password="gmha-admin-${$}"
recovery_password="gmha-recovery-${$}"
group_name="aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
nodes=("${prefix}-n1" "${prefix}-n2" "${prefix}-n3")
router="${prefix}-router"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    for node in "${nodes[@]}"; do
      printf '%s\n' "----- ${node} -----" >&2
      docker logs "$node" 2>&1 | tail -n 80 >&2 || true
    done
    docker logs "$router" 2>&1 | tail -n 80 >&2 || true
  fi
  docker rm -f "$router" "${nodes[@]}" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  rm -rf -- "$router_stage"
  return "$status"
}
trap cleanup EXIT

mysql_root() {
  local node="$1"
  shift
  docker exec "$node" mysql --protocol=socket -uroot "-p${root_password}" --batch --raw --skip-column-names "$@"
}

wait_mysql() {
  local node="$1"
  for _ in $(seq 1 90); do
    if mysql_root "$node" --execute "SELECT 1" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  docker logs "$node" >&2 || true
  return 1
}

wait_group_size() {
  local node="$1"
  local expected="$2"
  for _ in $(seq 1 120); do
    if [ "$(mysql_root "$node" --execute "SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE'")" = "$expected" ]; then
      return 0
    fi
    sleep 1
  done
  mysql_root "$node" --execute "SELECT MEMBER_HOST,MEMBER_STATE,MEMBER_ROLE FROM performance_schema.replication_group_members" >&2 || true
  return 1
}

mysqlsh_admin() {
  local node="$1"
  local script="$2"
  {
    for _ in $(seq 1 12); do
      printf '%s\n' "$admin_password"
    done
  } | docker exec -i "$node" mysqlsh \
    --js \
    --passwords-from-stdin \
    --uri "gmha_admin@127.0.0.1:3306" \
    --execute "$script"
}

docker network create "$network" >/dev/null
image_arch="$(docker image inspect "$mysql_image" --format '{{.Architecture}}')"
case "$image_arch" in
  arm64) router_archive="${MYSQL_ROUTER_ARCHIVE:-${repo_root}/software/mysql-router/mysql-router-9.7.1-linux-glibc2.28-aarch64.tar.xz}" ;;
  amd64) router_archive="${MYSQL_ROUTER_ARCHIVE:-${repo_root}/software/mysql-router/mysql-router-9.7.1-linux-glibc2.28-x86_64.tar.xz}" ;;
  *) printf 'unsupported Docker image architecture: %s\n' "$image_arch" >&2; exit 1 ;;
esac
tar -xJf "$router_archive" -C "$router_stage"
router_home="$(find "$router_stage" -mindepth 1 -maxdepth 1 -type d -print -quit)"
[ -x "${router_home}/bin/mysqlrouter" ]

for index in 1 2 3; do
  node="${nodes[$((index - 1))]}"
  docker run -d \
    --name "$node" \
    --hostname "n${index}" \
    --network "$network" \
    --network-alias "n${index}" \
    -e MYSQL_ROOT_PASSWORD="$root_password" \
    -e MYSQL_ROOT_HOST="%" \
    "$mysql_image" \
    --server-id="$index" \
    --report-host="n${index}" \
    --gtid-mode=ON \
    --enforce-gtid-consistency=ON \
    --log-bin=mysql-bin \
    --binlog-format=ROW \
    --log-replica-updates=ON \
    --plugin-load-add=group_replication.so \
    --loose-group-replication-start-on-boot=OFF \
    --loose-group-replication-group-name="$group_name" \
    --loose-group-replication-local-address="n${index}:33061" \
    --loose-group-replication-group-seeds="n1:33061,n2:33061,n3:33061" \
    --loose-group-replication-single-primary-mode=ON \
    --loose-group-replication-enforce-update-everywhere-checks=OFF \
    --loose-group-replication-ssl-mode=REQUIRED \
    --loose-group-replication-recovery-use-ssl=ON \
    --loose-group-replication-ip-allowlist=AUTOMATIC \
    >/dev/null
done

for node in "${nodes[@]}"; do
  wait_mysql "$node"
  mysql_root "$node" --execute "
    SET SQL_LOG_BIN=0;
    CREATE USER IF NOT EXISTS 'gmha_recovery'@'%' IDENTIFIED BY '${recovery_password}';
    GRANT REPLICATION SLAVE ON *.* TO 'gmha_recovery'@'%';
    CREATE USER IF NOT EXISTS 'gmha_admin'@'%' IDENTIFIED BY '${admin_password}';
    GRANT ALL PRIVILEGES ON *.* TO 'gmha_admin'@'%' WITH GRANT OPTION;
    GRANT CONNECTION_ADMIN,SYSTEM_VARIABLES_ADMIN,REPLICATION_SLAVE_ADMIN,REPLICATION_APPLIER,BACKUP_ADMIN,CLONE_ADMIN,GROUP_REPLICATION_ADMIN,PERSIST_RO_VARIABLES_ADMIN ON *.* TO 'gmha_admin'@'%' WITH GRANT OPTION;
    SET SQL_LOG_BIN=1;
    CHANGE REPLICATION SOURCE TO SOURCE_USER='gmha_recovery',SOURCE_PASSWORD='${recovery_password}' FOR CHANNEL 'group_replication_recovery';
  "
done
mysql_root "${nodes[1]}" --execute "RESET BINARY LOGS AND GTIDS"
mysql_root "${nodes[2]}" --execute "RESET BINARY LOGS AND GTIDS"

mysql_root "${nodes[0]}" --execute "SET GLOBAL group_replication_bootstrap_group=ON"
if ! mysql_root "${nodes[0]}" --execute "START GROUP_REPLICATION"; then
  mysql_root "${nodes[0]}" --execute "SET GLOBAL group_replication_bootstrap_group=OFF" || true
  exit 1
fi
mysql_root "${nodes[0]}" --execute "SET GLOBAL group_replication_bootstrap_group=OFF"
mysql_root "${nodes[1]}" --execute "START GROUP_REPLICATION"
mysql_root "${nodes[2]}" --execute "START GROUP_REPLICATION"
wait_group_size "${nodes[0]}" 3

mysqlsh_admin "${nodes[0]}" 'var c=dba.createCluster("gmha_acceptance",{adoptFromGR:true}); print(JSON.stringify(c.status({extended:1})));' >/dev/null

docker run -d \
  --name "$router" \
  --hostname router \
  --user 999:999 \
  --network "$network" \
  --network-alias router \
  -e ADMIN_PASSWORD="$admin_password" \
  -v "${router_home}:/opt/mysqlrouter:ro" \
  "$mysql_image" \
  sh -lc 'set -eu
    printf "%s\n" "$ADMIN_PASSWORD" | /opt/mysqlrouter/bin/mysqlrouter --bootstrap gmha_admin@n1:3306 --directory /tmp/mysqlrouter --force --name gmha_acceptance_router
    exec /opt/mysqlrouter/bin/mysqlrouter --config /tmp/mysqlrouter/mysqlrouter.conf' \
  >/dev/null

for _ in $(seq 1 90); do
  if docker exec "${nodes[0]}" mysql -hrouter -P6446 -ugmha_admin "-p${admin_password}" --connect-timeout=2 --execute "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${nodes[0]}" mysql -hrouter -P6446 -ugmha_admin "-p${admin_password}" --execute "
  CREATE DATABASE IF NOT EXISTS gmha_acceptance;
  CREATE TABLE IF NOT EXISTS gmha_acceptance.probe(id INT PRIMARY KEY, phase VARCHAR(32) NOT NULL);
  INSERT INTO gmha_acceptance.probe VALUES (1,'initial');
"
for node in "${nodes[@]}"; do
  [ "$(mysql_root "$node" --execute "SELECT COUNT(*) FROM gmha_acceptance.probe WHERE id=1 AND phase='initial'")" = "1" ]
done

docker stop "${nodes[0]}" >/dev/null
wait_group_size "${nodes[1]}" 2
for _ in $(seq 1 90); do
  if docker exec "${nodes[1]}" mysql -hrouter -P6446 -ugmha_admin "-p${admin_password}" --connect-timeout=2 --execute "INSERT INTO gmha_acceptance.probe VALUES (2,'failover')" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
[ "$(mysql_root "${nodes[1]}" --execute "SELECT COUNT(*) FROM gmha_acceptance.probe WHERE id=2 AND phase='failover'")" = "1" ]
[ "$(mysql_root "${nodes[2]}" --execute "SELECT COUNT(*) FROM gmha_acceptance.probe WHERE id=2 AND phase='failover'")" = "1" ]

docker start "${nodes[0]}" >/dev/null
wait_mysql "${nodes[0]}"
mysql_root "${nodes[0]}" --execute "START GROUP_REPLICATION"
wait_group_size "${nodes[1]}" 3
[ "$(mysql_root "${nodes[0]}" --execute "SELECT COUNT(*) FROM gmha_acceptance.probe")" = "2" ]
docker exec "${nodes[1]}" mysql -hrouter -P6447 -ugmha_admin "-p${admin_password}" --connect-timeout=2 --execute "SELECT COUNT(*) FROM gmha_acceptance.probe" >/dev/null

mysqlsh_admin "${nodes[1]}" 'var c=dba.getCluster(); c.setPrimaryInstance("n1:3306"); c.rescan({addUnmanaged:true,removeObsolete:true}); c.resetRecoveryAccountsPassword(); print(JSON.stringify(c.status({extended:1})));' >/dev/null
for _ in $(seq 1 60); do
  [ "$(mysql_root "${nodes[0]}" --execute "SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_HOST='n1' AND MEMBER_ROLE='PRIMARY' AND MEMBER_STATE='ONLINE'")" = "1" ] && break
  sleep 1
done
[ "$(mysql_root "${nodes[0]}" --execute "SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_HOST='n1' AND MEMBER_ROLE='PRIMARY' AND MEMBER_STATE='ONLINE'")" = "1" ]

mysql_root "${nodes[2]}" --execute "STOP GROUP_REPLICATION"
mysqlsh_admin "${nodes[0]}" 'var c=dba.getCluster(); c.rejoinInstance("gmha_admin@n3:3306"); print(JSON.stringify(c.status({extended:1})));' >/dev/null
wait_group_size "${nodes[0]}" 3

for node in "${nodes[@]}"; do
  mysql_root "$node" --execute "STOP GROUP_REPLICATION"
done
mysqlsh_admin "${nodes[1]}" 'var c=dba.rebootClusterFromCompleteOutage("gmha_acceptance",{primary:"n2:3306"}); print(JSON.stringify(c.status({extended:1})));' >/dev/null
wait_group_size "${nodes[1]}" 3
docker exec "${nodes[1]}" mysql -hrouter -P6446 -ugmha_admin "-p${admin_password}" --connect-timeout=2 --execute "INSERT INTO gmha_acceptance.probe VALUES (3,'outage-recovery')" >/dev/null
for node in "${nodes[@]}"; do
  [ "$(mysql_root "$node" --execute "SELECT COUNT(*) FROM gmha_acceptance.probe WHERE id=3 AND phase='outage-recovery'")" = "1" ]
done

roles="$(mysql_root "${nodes[1]}" --execute "SELECT CONCAT(MEMBER_HOST,':',MEMBER_STATE,':',MEMBER_ROLE) FROM performance_schema.replication_group_members ORDER BY MEMBER_HOST")"
printf '%s\n' "$roles"
printf 'MGR_ROUTER_SMOKE_OK\n'
