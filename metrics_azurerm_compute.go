package main

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/resources/mgmt/subscriptions"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	prometheusCommon "github.com/webdevops/go-prometheus-common"
)

type MetricsCollectorAzureRmCompute struct {
	CollectorProcessorGeneral

	prometheus struct {
		vm    *prometheus.GaugeVec
		vmOs  *prometheus.GaugeVec
		vmNic *prometheus.GaugeVec

		vmss         *prometheus.GaugeVec
		vmssCapacity *prometheus.GaugeVec
	}
}

func (m *MetricsCollectorAzureRmCompute) Setup(collector *CollectorGeneral) {
	m.CollectorReference = collector

	m.prometheus.vm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_vm_info",
			Help: "Azure ResourceManager VMs",
		},
		append(
			[]string{
				"resourceID",
				"subscriptionID",
				"location",
				"resourceGroup",
				"vmID",
				"vmName",
				"vmType",
				"vmSize",
				"vmProvisioningState",
			},
			azureResourceTags.prometheusLabels...,
		),
	)
	prometheus.MustRegister(m.prometheus.vm)

	m.prometheus.vmOs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_vm_os",
			Help: "Azure ResourceManager VM OS",
		},
		[]string{
			"vmID",
			"imagePublisher",
			"imageSku",
			"imageOffer",
			"imageVersion",
		},
	)
	prometheus.MustRegister(m.prometheus.vmOs)

	m.prometheus.vmNic = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_vm_nic",
			Help: "Azure ResourceManager VM NIC",
		},
		[]string{
			"vmID",
			"resourceID",
			"isPrimary",
		},
	)
	prometheus.MustRegister(m.prometheus.vmNic)

	m.prometheus.vmss = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_vmss_info",
			Help: "Azure ResourceManager VMSS",
		},
		append(
			[]string{
				"resourceID",
				"subscriptionID",
				"location",
				"resourceGroup",
				"vmssName",
				"vmssType",
				"vmssProvisioningState",
			},
			azureResourceTags.prometheusLabels...,
		),
	)
	prometheus.MustRegister(m.prometheus.vmss)

	m.prometheus.vmssCapacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_vmss_capacity",
			Help: "Azure ResourceManager VMSS",
		},
		[]string{
			"resourceID",
			"subscriptionID",
			"location",
			"resourceGroup",
			"vmssName",
		},
	)
	prometheus.MustRegister(m.prometheus.vmssCapacity)
}

func (m *MetricsCollectorAzureRmCompute) Reset() {
	m.prometheus.vm.Reset()
	m.prometheus.vmOs.Reset()
	m.prometheus.vmNic.Reset()
	m.prometheus.vmss.Reset()
	m.prometheus.vmssCapacity.Reset()
}

func (m *MetricsCollectorAzureRmCompute) Collect(ctx context.Context, logger *log.Entry, callback chan<- func(), subscription subscriptions.Subscription) {
	m.collectAzureVm(ctx, logger, callback, subscription)
	m.collectAzureVmss(ctx, logger, callback, subscription)
}

func (m *MetricsCollectorAzureRmCompute) collectAzureVm(ctx context.Context, logger *log.Entry, callback chan<- func(), subscription subscriptions.Subscription) {
	client := compute.NewVirtualMachinesClient(*subscription.SubscriptionID)
	client.Authorizer = AzureAuthorizer

	list, err := client.ListAllComplete(ctx, "")

	if err != nil {
		logger.Panic(err)
	}

	infoMetric := prometheusCommon.NewMetricsList()
	osMetric := prometheusCommon.NewMetricsList()
	nicMetric := prometheusCommon.NewMetricsList()

	for list.NotDone() {
		val := list.Value()

		infoLabels := prometheus.Labels{
			"resourceID":          *val.ID,
			"subscriptionID":      *subscription.SubscriptionID,
			"location":            stringPtrToString(val.Location),
			"resourceGroup":       extractResourceGroupFromAzureId(*val.ID),
			"vmID":                stringPtrToString(val.VMID),
			"vmName":              stringPtrToString(val.Name),
			"vmType":              stringPtrToString(val.Type),
			"vmSize":              string(val.VirtualMachineProperties.HardwareProfile.VMSize),
			"vmProvisioningState": stringPtrToString(val.ProvisioningState),
		}
		infoLabels = azureResourceTags.appendPrometheusLabel(infoLabels, val.Tags)
		infoMetric.AddInfo(infoLabels)

		if val.StorageProfile != nil {
			osMetric.AddInfo(prometheus.Labels{
				"vmID":           *val.VMID,
				"imagePublisher": stringPtrToString(val.StorageProfile.ImageReference.Publisher),
				"imageSku":       stringPtrToString(val.StorageProfile.ImageReference.Sku),
				"imageOffer":     stringPtrToString(val.StorageProfile.ImageReference.Offer),
				"imageVersion":   stringPtrToString(val.StorageProfile.ImageReference.Version),
			})
		}

		if val.NetworkProfile != nil && val.NetworkProfile.NetworkInterfaces != nil {
			for _, nic := range *val.NetworkProfile.NetworkInterfaces {
				var nicIsPrimary *bool
				if nic.NetworkInterfaceReferenceProperties != nil {
					nicIsPrimary = nic.NetworkInterfaceReferenceProperties.Primary
				}

				nicMetric.AddInfo(prometheus.Labels{
					"vmID":       *val.VMID,
					"resourceID": stringPtrToString(nic.ID),
					"isPrimary":  boolPtrToString(nicIsPrimary),
				})
			}
		}

		if list.NextWithContext(ctx) != nil {
			break
		}
	}

	callback <- func() {
		infoMetric.GaugeSet(m.prometheus.vm)
		osMetric.GaugeSet(m.prometheus.vmOs)
		nicMetric.GaugeSet(m.prometheus.vmNic)
	}
}

func (m *MetricsCollectorAzureRmCompute) collectAzureVmss(ctx context.Context, logger *log.Entry, callback chan<- func(), subscription subscriptions.Subscription) {
	client := compute.NewVirtualMachineScaleSetsClient(*subscription.SubscriptionID)
	client.Authorizer = AzureAuthorizer

	list, err := client.ListAllComplete(ctx)

	if err != nil {
		logger.Panic(err)
	}

	infoMetric := prometheusCommon.NewMetricsList()
	capacityMetric := prometheusCommon.NewMetricsList()

	for list.NotDone() {
		val := list.Value()

		infoLabels := prometheus.Labels{
			"resourceID":            *val.ID,
			"subscriptionID":        *subscription.SubscriptionID,
			"location":              stringPtrToString(val.Location),
			"resourceGroup":         extractResourceGroupFromAzureId(*val.ID),
			"vmssName":              stringPtrToString(val.Name),
			"vmssType":              stringPtrToString(val.Type),
			"vmssProvisioningState": stringPtrToString(val.ProvisioningState),
		}
		infoLabels = azureResourceTags.appendPrometheusLabel(infoLabels, val.Tags)
		infoMetric.AddInfo(infoLabels)

		if val.Sku != nil && val.Sku.Capacity != nil {
			capacityMetric.Add(prometheus.Labels{
				"resourceID":     *val.ID,
				"subscriptionID": *subscription.SubscriptionID,
				"location":       stringPtrToString(val.Location),
				"resourceGroup":  extractResourceGroupFromAzureId(*val.ID),
				"vmssName":       stringPtrToString(val.Name),
			}, float64(*val.Sku.Capacity))
		}

		if list.NextWithContext(ctx) != nil {
			break
		}
	}

	callback <- func() {
		infoMetric.GaugeSet(m.prometheus.vmss)
		capacityMetric.GaugeSet(m.prometheus.vmssCapacity)
	}
}
