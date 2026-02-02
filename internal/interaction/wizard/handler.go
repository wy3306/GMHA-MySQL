package wizard

import (
	"GMHA-MySQL/internal/service"
	"GMHA-MySQL/internal/ui"
	"context"
	"fmt"
	"strconv"
)

// Run 启动引导式交互模式
func Run(svc service.ClusterService) {
	prompter := ui.NewPrompter()
	prompter.PrintHeader("GMHA-MySQL Interactive Setup")

	for {
		fmt.Println("\nAvailable Actions:")
		fmt.Println("1. Add New Cluster")
		fmt.Println("2. List Clusters")
		fmt.Println("q. Quit")

		choice := prompter.Ask("Select an action", "1")

		switch choice {
		case "1":
			addClusterFlow(prompter, svc)
		case "2":
			listClustersFlow(prompter, svc)
		case "q", "quit", "exit":
			fmt.Println("Bye!")
			return
		default:
			fmt.Println("Invalid choice.")
		}
	}
}

func addClusterFlow(p *ui.Prompter, svc service.ClusterService) {
	p.PrintHeader("Step 1: Cluster Basic Info")

	input := service.CreateClusterInput{}

	input.Name = p.Ask("Cluster Name", "")
	if input.Name == "" {
		fmt.Println("Error: Cluster Name is required.")
		return
	}

	input.Description = p.Ask("Description", "My MySQL Cluster")
	input.VIP = p.Ask("VIP (Virtual IP)", "")
	input.VIPEnabled = p.AskBool("Enable VIP now?", true)
	input.HasL3Switch = p.AskBool("Is there a Layer 3 Switch?", false)

	p.PrintHeader("Step 2: Add Machines")
	for {
		addMachine := p.AskBool("Add a machine to this cluster?", true)
		if !addMachine {
			break
		}

		m := service.CreateMachineInput{}
		m.IP = p.Ask("Machine IP", "")
		if m.IP == "" {
			fmt.Println("IP is required.")
			continue
		}

		portStr := p.Ask("SSH Port", "22")
		port, _ := strconv.Atoi(portStr)
		m.SSHPort = port

		m.SSHUser = p.Ask("SSH User", "root")
		m.SSHPassword = p.Ask("SSH Password", "") // TODO: Mask input

		input.Machines = append(input.Machines, m)
		p.PrintInfo("Machine %s added to list.", m.IP)
	}

	p.PrintHeader("Step 3: Confirmation")
	fmt.Printf("Cluster Name: %s\n", input.Name)
	fmt.Printf("VIP: %s (Enabled: %v)\n", input.VIP, input.VIPEnabled)
	fmt.Printf("Machines Count: %d\n", len(input.Machines))

	confirm := p.AskBool("Confirm create?", true)
	if !confirm {
		fmt.Println("Operation cancelled.")
		return
	}

	// 调用 Service (内核)
	cluster, err := svc.CreateCluster(context.Background(), input)
	if err != nil {
		fmt.Printf("Error creating cluster: %v\n", err)
	} else {
		p.PrintInfo("Success! Cluster created with ID: %s", cluster.ID)
	}
}

func listClustersFlow(p *ui.Prompter, svc service.ClusterService) {
	clusters, err := svc.ListClusters(context.Background())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	p.PrintHeader("Cluster List")
	if len(clusters) == 0 {
		fmt.Println("No clusters found.")
		return
	}

	for _, c := range clusters {
		fmt.Printf("- [%s] %s (VIP: %s)\n", c.ID, c.Name, c.VIP)
	}
}
