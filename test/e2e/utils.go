package e2e

import (
	goctx "context"
	"fmt"
	"os"
	"testing"
	"time"

	operator "github.com/instaclustr/cassandra-operator/pkg/apis/cassandraoperator/v1alpha1"
	"github.com/instaclustr/cassandra-operator/pkg/common/nodestate"
	"github.com/instaclustr/cassandra-operator/pkg/sidecar"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 600
	cleanupRetryInterval = time.Second * 3
	cleanupTimeout       = time.Second * 60

	testCassandraImage = "OPERATOR_TEST_CASSANDRA_IMAGE"
	testSidecarImage   = "OPERATOR_TEST_SIDECAR_IMAGE"
	pullSecret         = "OPERATOR_TEST_PULL_SECRET"
)

func initialise(t *testing.T, configMaps []*v1.ConfigMap) (*framework.TestCtx, *framework.Framework, *framework.CleanupOptions, string) {

	ctx := framework.NewTestCtx(t)

	if err := ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: timeout}); err != nil {
		t.Fatalf("Failed to initialize cluster resources: %v", err)
	}

	t.Log("Initialized cluster resources")

	namespace, err := ctx.GetNamespace()

	if err != nil {
		t.Fatalf("Could not obtain namespace: %v", err)
	}

	f := framework.Global

	if err := e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "cassandra-operator", 1, retryInterval, timeout); err != nil {
		t.Fatalf("Timeout of Cassandra operator deployment has occurred: %v", err)
	}

	for _, configMap := range configMaps {
		configMap.Namespace = namespace
		_ = f.Client.Create(goctx.TODO(), configMap, &framework.CleanupOptions{
			TestContext:   ctx,
			Timeout:       timeout,
			RetryInterval: cleanupRetryInterval,
		})
	}

	cleanupOptions := framework.CleanupOptions{
		TestContext:   ctx,
		Timeout:       cleanupTimeout,
		RetryInterval: cleanupRetryInterval,
	}

	return ctx, f, &cleanupOptions, namespace
}

func updateCassandraDataCenter(t *testing.T, f *framework.Framework, cassandraDC *operator.CassandraDataCenter) {
	if err := f.Client.Update(goctx.TODO(), cassandraDC); err != nil {
		t.Fatalf("Unable to update Cassandra DC object of %d node(s): %v", cassandraDC.Spec.Nodes, err)
	}
}

func createCassandraDataCenter(t *testing.T, f *framework.Framework, cleanupOptions *framework.CleanupOptions, cassandraDC *operator.CassandraDataCenter) {
	if err := f.Client.Create(goctx.TODO(), cassandraDC, cleanupOptions); err != nil {
		t.Fatalf("Unable to create Cassandra DC object of %d node(s): %v", cassandraDC.Spec.Nodes, err)
	}
}

func checkAllNodesInNormalMode(t *testing.T, f *framework.Framework, namespace string) {

	clients, err := podsSidecars(f, namespace)

	if err != nil {
		t.Fatalf("Error getting pod's sidecars: %v", err)
	}

	pollingErr := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		for _, client := range clients {
			if status, err := client.Status(); err != nil {
				return false, err
			} else if status.NodeState != nodestate.NORMAL {
				fmt.Printf("Node '%v' is in status '%v', waiting to get it to %v\n", client.Host, status.NodeState, nodestate.NORMAL)
				return false, nil
			}
		}

		return true, nil
	})

	if pollingErr != nil {
		t.Fatalf("All nodes were not transitioned to state %s: %v", nodestate.NORMAL, pollingErr)
	}
}

func waitForStatefulset(t *testing.T, f *framework.Framework, namespace, statefulSetName string, expectedReplicas int32) {

	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		statefulSet, err := f.KubeClient.AppsV1().StatefulSets(namespace).Get(statefulSetName, metav1.GetOptions{})

		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s statefulset in namespace %s", statefulSetName, namespace)
				return false, nil
			}
			return false, err
		}

		replicas := statefulSet.Status.Replicas
		readyReplicas := statefulSet.Status.ReadyReplicas

		if replicas != expectedReplicas {
			t.Logf("All replicas of %s statefulset in namespace %s are started but some of them are not ready - (%d/%d)", statefulSetName, namespace, readyReplicas, expectedReplicas)
			return false, nil
		}

		if replicas != readyReplicas {
			t.Logf("Waiting for full availability of %s statefulset in namespace %s - (%d/%d)", statefulSetName, namespace, readyReplicas, replicas)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		t.Fatalf("Timeout for statefulset %s in namespace %s has occurred: %v", statefulSetName, namespace, err)
	}

	t.Logf("statefulset %s in namespace %s is fully available.", statefulSetName, namespace)
}

func podsSidecars(f *framework.Framework, namespace string) (map[*v1.Pod]*sidecar.Client, error) {

	operatorSelector := map[string]string{"app.kubernetes.io/managed-by": "com.instaclustr.cassandra-operator"}
	pods, err := f.KubeClient.CoreV1().Pods(namespace).List(metav1.ListOptions{LabelSelector: labels.Set(operatorSelector).String()})

	if err != nil {
		return nil, err
	}

	return sidecar.SidecarClients(pods.Items, &sidecar.DefaultSidecarClientOptions), nil
}

func defaultNewCassandraDataCenterList() *operator.CassandraDataCenterList {
	return &operator.CassandraDataCenterList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CassandraDataCenter",
			APIVersion: "cassandraoperator.instaclustr.com/v1alpha1",
		},
	}
}

func defaultMinimalCassandraDatacenter(name string, namespace string) *operator.CassandraDataCenter {
	return &operator.CassandraDataCenter{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CassandraDataCenter",
			APIVersion: "cassandraoperator.instaclustr.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func defaultNewCassandraDataCenter(name string, namespace string, nodes int32, racks []operator.Rack) *operator.CassandraDataCenter {

	// should be done via flags but seems to be not supported yet https://github.com/operator-framework/operator-sdk/issues/1476

	cassandraImageName := parseEnvProperty(testCassandraImage, "gcr.io/cassandra-operator/cassandra:3.11.3")
	sidecarImageName := parseEnvProperty(testSidecarImage, "gcr.io/cassandra-operator/cassandra-sidecar:latest")
	pullSecret := parseEnvProperty(pullSecret, "")

	disk, _ := resource.ParseQuantity("500Mi")
	memory, _ := resource.ParseQuantity("1Gi")

	cassandraDC := &operator.CassandraDataCenter{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CassandraDataCenter",
			APIVersion: "cassandraoperator.instaclustr.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: operator.CassandraDataCenterSpec{
			Nodes:             nodes,
			Racks:             racks,
			CassandraImage:    cassandraImageName,
			SidecarImage:      sidecarImageName,
			PrometheusSupport: false,
			ImagePullPolicy:   v1.PullAlways,
			DataVolumeClaimSpec: &v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{
					"ReadWriteOnce",
				},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						"storage": disk,
					},
				},
			},
			Resources: &v1.ResourceRequirements{
				Limits: v1.ResourceList{
					"memory": memory,
				},
				Requests: v1.ResourceList{
					"memory": memory,
				},
			},
		},
	}

	if pullSecret != "" {
		cassandraDC.Spec.ImagePullSecrets = []v1.LocalObjectReference{
			{
				Name: pullSecret,
			},
		}
	}

	return cassandraDC
}

func parseEnvProperty(name string, defaultValue string) string {

	value := os.Getenv(name)
	if value == "" {
		return defaultValue
	}

	return value
}
