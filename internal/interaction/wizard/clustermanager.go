package wizard

import (
	"fmt"
)

func ClusterManager() {
	for {
		fmt.Println("1.添加集群描述")
		fmt.Println("2.修改集群描述")
		fmt.Println("3.设置管理端口")
		fmt.Println("4.修改管理端口")
		fmt.Println("5.重启集群")
		fmt.Println("6.停用集群")
		fmt.Println("7.删除集群")
		fmt.Println("8.启动集群")
		fmt.Println("q.退出")
	}
}
