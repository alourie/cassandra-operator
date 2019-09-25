package cassandradatacenter

import (
	"context"
	"errors"
	"fmt"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	monclientv1 "github.com/coreos/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func createOrUpdateNodesService(rctx *reconciliationRequestContext) (*corev1.Service, error) {
	nodesService := &corev1.Service{ObjectMeta: DataCenterResourceMetadata(rctx.cdc, "nodes")}

	logger := rctx.logger.WithValues("Service.Name", nodesService.Name)

	opresult, err := controllerutil.CreateOrUpdate(context.TODO(), rctx.client, nodesService, func(_ runtime.Object) error {
		nodesService.Spec = corev1.ServiceSpec{
			ClusterIP: "None",
			Ports:     ports{cqlPort, jmxPort}.asServicePorts(),
			Selector:  DataCenterLabels(rctx.cdc),
		}

		if rctx.cdc.Spec.PrometheusSupport {
			nodesService.Spec.Ports = append(nodesService.Spec.Ports, prometheusPort.asServicePort())
		}

		for k, v := range rctx.cdc.Spec.NodesServiceLabels {
			nodesService.Labels[k] = v
		}

		if err := controllerutil.SetControllerReference(rctx.cdc, nodesService, rctx.scheme); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Only log if something has changed
	if opresult != controllerutil.OperationResultNone {
		logger.Info(fmt.Sprintf("Service %s %s.", nodesService.Name, opresult))
	}

	return nodesService, err
}

func createOrUpdateSeedNodesService(rctx *reconciliationRequestContext) (*corev1.Service, error) {
	seedNodesService := &corev1.Service{ObjectMeta: DataCenterResourceMetadata(rctx.cdc, "seeds")}

	logger := rctx.logger.WithValues("Service.Name", seedNodesService.Name)

	opresult, err := controllerutil.CreateOrUpdate(context.TODO(), rctx.client, seedNodesService, func(_ runtime.Object) error {
		seedNodesService.Spec = corev1.ServiceSpec{
			ClusterIP:                "None",
			Ports:                    internodePort.asServicePorts(),
			Selector:                 DataCenterLabels(rctx.cdc),
			PublishNotReadyAddresses: true,
		}

		seedNodesService.Annotations = map[string]string{
			"service.alpha.kubernetes.io/tolerate-unready-endpoints": "true",
		}

		if err := controllerutil.SetControllerReference(rctx.cdc, seedNodesService, rctx.scheme); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Only log if something has changed
	if opresult != controllerutil.OperationResultNone {
		logger.Info(fmt.Sprintf("Service %s %s.", seedNodesService.Name, opresult))
	}

	return seedNodesService, err
}

func createOrUpdatePrometheusServiceMonitor(svc *corev1.Service) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}

	// Verify ServiceMonitor CRD exists on cluster.
	dc := discovery.NewDiscoveryClientForConfigOrDie(cfg)
	apiVersion := "monitoring.coreos.com/v1"
	kind := "ServiceMonitor"

	exists, err := k8sutil.ResourceExists(dc, apiVersion, kind)
	if err != nil {
		return err
	}
	if !exists {
		return metrics.ErrServiceMonitorNotPresent
	}

	sm, err := generateServiceMonitor(svc, []string{prometheusPort.name})
	if err != nil {
		return err
	}

	mclient := monclientv1.NewForConfigOrDie(cfg)

	_, err = mclient.ServiceMonitors(svc.Namespace).Create(sm)
	if err != nil {
		if k8serr.IsAlreadyExists(err) {
			log.Info(fmt.Sprintf("ServiceMonitor %s already exists, skipping creation", svc.Name))
			return nil
		} else {
			return err
		}
	}

	return nil
}

// generateServiceMonitor generates a prometheus-operator ServiceMonitor object
// based on the passed Service object and a slice of port names.
func generateServiceMonitor(s *v1.Service, portNames []string) (*monitoringv1.ServiceMonitor, error) {
	labels := make(map[string]string)
	for k, v := range s.ObjectMeta.Labels {
		labels[k] = v
	}

	var endpoints []monitoringv1.Endpoint
	for _, pn := range portNames {
		found := false
		for _, port := range s.Spec.Ports {
			if port.Name == pn {
				found = true
				endpoints = append(endpoints, monitoringv1.Endpoint{Port: port.Name})
			}
		}
		if !found {
			log.Info(fmt.Sprintf("Service %s doesn't have a port named '%s'", s.Name, pn))
		}
	}

	if len(endpoints) == 0 {
		return nil, errors.New("ServiceMonitor has no valid endpoints")
	}

	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.ObjectMeta.Name,
			Namespace: s.ObjectMeta.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					BlockOwnerDeletion: boolPointer(true),
					Controller:         boolPointer(true),
					Kind:               "Service",
					Name:               s.Name,
					UID:                s.UID,
				},
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: labels,
			},
			Endpoints: endpoints,
		},
	}, nil
}
