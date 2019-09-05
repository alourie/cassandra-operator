package e2e

import (
	"fmt"
	"testing"

	"github.com/instaclustr/cassandra-operator/pkg/apis"
	"github.com/instaclustr/cassandra-operator/pkg/apis/cassandraoperator/v1alpha1"
	"github.com/instaclustr/cassandra-operator/pkg/common/cluster"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
)

const (
	cdcName         = "test-dc-cassandra"
	statefulSetName = "cassandra-test-dc-cassandra"
)

func TestCassandra(t *testing.T) {

	if err := framework.AddToFrameworkScheme(apis.AddToScheme, defaultNewCassandraDataCenterList()); err != nil {
		t.Fatalf("Failed to add custom resource scheme to framework: %v", err)
	}

	t.Run("scaling up", CassandraScalingUp)
	t.Run("scaling down", CassandraScalingDown)
}

func deployCassandra(t *testing.T, nodes, racks int32) (*framework.TestCtx, *framework.Framework, *v1alpha1.CassandraDataCenter, string) {

	t.Log("Running test " + t.Name())

	ctx, f, cleanupOptions, namespace := initialise(t)

	cassandraDC := defaultNewCassandraDataCenter(cdcName, namespace, nodes, racks)
	createCassandraDataCenter(t, f, cleanupOptions, cassandraDC)
	checkStatefulSets(t, f, namespace, nodes, racks)
	checkAllNodesInNormalMode(t, f, namespace)

	return ctx, f, cassandraDC, namespace
}

func CassandraScalingUp(t *testing.T) {
	t.Log("Running test " + t.Name())
	scaleCluster(t, 2, 3)
}

func CassandraScalingDown(t *testing.T) {
	t.Log("Running test " + t.Name())
	scaleCluster(t, 3, 2)
}

func scaleCluster(t *testing.T, from, to int32) {

	// Let's make it easy
	racks := int32(3)

	fmt.Printf("Deploying cluster with %v nodes into %v racks\n", from, racks)
	ctx, f, cassandraDC, namespace := deployCassandra(t, from, racks)

	// scale, from "from" to "to" nodes

	fmt.Printf("Done deployment, scaling from %v to %v\n", from, to)
	cassandraDC.Spec.Nodes = to
	updateCassandraDataCenter(t, f, cassandraDC)

	// wait until scaling is done and check all racks and nodes are in normal state
	checkStatefulSets(t, f, cassandraDC.Namespace, to, racks)
	checkAllNodesInNormalMode(t, f, namespace)

	// Cleanup after test
	ctx.Cleanup()
}

func checkStatefulSets(t *testing.T, f *framework.Framework, namespace string, nodes, racks int32) {
	zone := "failure-domain.beta.kubernetes.io/zone"
	cdcSpec := v1alpha1.CassandraDataCenterSpec{Nodes: nodes, Racks: []v1alpha1.Rack{
		{Name: "west1-a", Labels: map[string]string{zone: "europe-west1-a"}},
		{Name: "west1-b", Labels: map[string]string{zone: "europe-west1-b"}},
		{Name: "west1-c", Labels: map[string]string{zone: "europe-west1-c"}},
	}}
	rackDistribution := cluster.BuildRacksDistribution(cdcSpec)
	for _, rack := range rackDistribution {
		waitForStatefulset(t, f, namespace, statefulSetName+"-"+rack.Name, rack.Replicas)
	}
}
