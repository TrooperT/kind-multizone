/*
Copyright © 2024 TrooperT <samuel.johnson.bungie@gmail.com>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"
	config "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/kind/pkg/cluster"
	kindcmd "sigs.k8s.io/kind/pkg/cmd"
	"sigs.k8s.io/kind/pkg/errors"

	"github.com/docker/docker/client"
	// "github.com/docker/docker/api/types/container"
	networkv1 "github.com/docker/docker/api/types/network"
)

const (
	topologyLabelRegion = "topology.kubernetes.io/region"
	topologyLabelZone   = "topology.kubernetes.io/zone"
	defaultName         = "kind-multizone"
	defaultZones        = 1
	defaultNodes        = 1
	defaultRetain       = false
	defaultControlPlane = 1
	defaultNodeImage			= "kindest/node:v1.31.4"
	// defaultNodeImage         = "aojea/kindnode:1.22rc"
	topologyZoneWorker       = "zone-workers-%d"
	topologyZoneControlPlane = "zone-controlplane-%d"
)

// createCmd represents the create command
var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a multi-zone KinD cluster",
	Long: `Create a multi-zone KinD cluster
A multi-zone cluster has nodes that span multiple availability zones`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// fmt.Println("create called")
		// return
		return ensureCluster(cmd)
	},
}

func init() {
	rootCmd.AddCommand(createCmd)

	createCmd.Flags().
		String("name",
			defaultName,
			fmt.Sprintf("Cluster name, overrides KIND_CLUSTER_NAME and config (default %s)", defaultName),
		)

	createCmd.Flags().
		Bool("retain",
			defaultRetain,
			fmt.Sprintf("Whether to retain nodes in case of cluster creation failure (default %t)", defaultRetain),
		)

	createCmd.Flags().
		Int("zones",
			defaultZones,
			fmt.Sprintf("Number of zones to create (default %d)", defaultZones),
		)

	createCmd.Flags().
		Int("nodes-per-zone",
			defaultNodes,
			fmt.Sprintf("Number of nodes to create per zone (default %d)", defaultNodes),
		)
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// createCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// createCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
func ensureCluster(cmd *cobra.Command) error {
	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return err
	}

	zones, err := cmd.Flags().GetInt("zones")
	if err != nil {
		return err
	}
	fmt.Printf("Number of zones: %d\n", zones)

	nodesPerZones, err := cmd.Flags().GetInt("nodes-per-zone")
	if err != nil {
		return err
	}
	fmt.Printf("Number of nodes per zones: %d\n", nodesPerZones)

	retain, err := cmd.Flags().GetBool("retain")

	// Setup logger and KinD provider
	logger := kindcmd.NewLogger()
	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(logger),
	)
	_, _ = provider.List()
	clusterNetworkName := fmt.Sprintf("kmz-%s", name)
	fmt.Printf("Cluster network: %s\n", clusterNetworkName)
	netID, err := ensureNetwork(clusterNetworkName, "", true)
	if err != nil {
		return err
	}
	fmt.Printf("Created network %s with ID: %s\n", clusterNetworkName, netID)
	os.Setenv("KIND_EXPERIMENTAL_DOCKER_NETWORK", clusterNetworkName)

	clusterConfig := &config.Cluster{
		Name:  name,
		Nodes: renderNodes(zones, nodesPerZones),
		// FeatureGates: map[string]bool{
		// 	"TopologyAwareHints": true,
		// },
	}
	err = provider.Create(
		name,
		cluster.CreateWithV1Alpha4Config(clusterConfig),
		// cluster.CreateWithNodeImage(flags.ImageName),
		// cluster.CreateWithRetain(flags.Retain),
		cluster.CreateWithRetain(retain),
		// cluster.CreateWithWaitForReady(flags.Wait),
		// cluster.CreateWithKubeconfigPath(flags.Kubeconfig),
		cluster.CreateWithDisplayUsage(true),
		// cluster.CreateWithDisplaySalutation(true),
	)
	if err != nil {
		return errors.Wrap(err, "Failed to create cluster")
	}

	os.Unsetenv("KIND_EXPERIMENTAL_DOCKER_NETWORK")

	return nil
}

func renderNodes(zones int, nodesPerZone int) []config.Node {
	nodes := []config.Node{}
	for i := 0; i < defaultControlPlane; i++ {
		n := config.Node{
			Role:  config.ControlPlaneRole,
			Image: defaultNodeImage,
			Labels: map[string]string{
				topologyLabelZone: fmt.Sprintf(topologyZoneControlPlane, i),
			},
		}
		nodes = append(nodes, n)
	}
	for i := 0; i < zones; i++ {
		for j := 0; j < nodesPerZone; j++ {
			n := config.Node{
				Role:  config.WorkerRole,
				Image: defaultNodeImage,
				Labels: map[string]string{
					topologyLabelZone: fmt.Sprintf(topologyZoneWorker, i),
				},
			}
			nodes = append(nodes, n)
		}
	}

	return nodes
}

func ensureNetwork(name string, subnet string, masquerade bool) (string, error) {
	apiClient, err := client.
		NewClientWithOpts(
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
		)
	if err != nil {
		fmt.Printf("%s", err)
		return "", err
	}
	networks, err := apiClient.NetworkList(context.Background(), networkv1.ListOptions{})
	if err != nil {
		fmt.Printf("%s", err)
		return "", err
	}
	// Loop over discovered network names and attempt delete if name matches
	// our chosen name
	for _, net := range networks {
		// fmt.Printf("%s %s\n", net.ID, net.Name)
		if net.Name == name {
			fmt.Printf("Duplicate network found with name: %s\n", name)
			fmt.Printf("Deleting and recreating network %s\n", name)
			err = deleteNetwork(apiClient, name, net.ID)
			if err != nil {
				return "", err
			}
		}
	}
	netID, err := createNetwork(apiClient, name, subnet, masquerade)
	if err != nil {
		return "", err
	}

	return netID, nil
}

func createNetwork(apiClient *client.Client, name string, subnet string, masquerade bool) (string, error) {
	options := &networkv1.CreateOptions{
		Attachable: true,
		Options: map[string]string{
			"com.docker.network.bridge.name":                 fmt.Sprintf("%s", name),
			"com.docker.network.driver.mtu":                  fmt.Sprintf("%d", 1500),
			"com.docker.network.bridge.enable_ip_masquerade": fmt.Sprintf("%t", masquerade),
		},
	}
	if subnet != "" {
		options.IPAM = &networkv1.IPAM{}
		_, cidr, err := net.ParseCIDR(subnet)
		if err != nil {
			return "", err
		}
		cidr.Mask = net.CIDRMask(27, 32)
		options.IPAM.Config = append(options.IPAM.Config, networkv1.IPAMConfig{
			Subnet:  subnet,
			IPRange: cidr.String(),
			// Docker will automatically set gateway to the 1st IP if unset
			// Gateway: ,
		})
	}
	resp, err := apiClient.NetworkCreate(context.Background(),
		name, *options)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func deleteNetwork(apiClient *client.Client, name string, netID string) error {
	if true {
		fmt.Printf("Deleting docker network: %s\n", name)
	}
	err := apiClient.NetworkRemove(context.Background(), netID)
	if err != nil {
		return err
	}
	return nil
}
