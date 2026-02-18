package incluster

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v9"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// getIngress assembles and returns the Ingress needed for the VICE analysis.
// It does not call the k8s API.
func (i *Incluster) getIngress(ctx context.Context, job *model.Job, svc *apiv1.Service, class string) (*netv1.Ingress, error) {
	var (
		rules       []netv1.IngressRule
		defaultPort int32
	)

	labels, err := i.jobInfo.JobLabels(ctx, job)
	if err != nil {
		return nil, err
	}
	ingressName := common.Subdomain(job.UserID, job.InvocationID)

	// Find the proxy port, use it as the default
	for _, port := range svc.Spec.Ports {
		if port.Name == constants.VICEProxyPortName {
			defaultPort = port.Port
		}
	}

	// Handle if the defaultPort isn't set yet.
	if defaultPort == 0 {
		return nil, fmt.Errorf("port %s was not found in the service", constants.VICEProxyPortName)
	}

	// default backend, should point at the VICE default backend, which redirects
	// users to the loading page.
	defaultBackend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: i.ViceDefaultBackendService,
			Port: netv1.ServiceBackendPort{
				Number: int32(i.ViceDefaultBackendServicePort),
			},
		},
	}

	// Backend for the service, not the default backend
	backend := &netv1.IngressBackend{
		Service: &netv1.IngressServiceBackend{
			Name: svc.Name,
			Port: netv1.ServiceBackendPort{
				Number: defaultPort,
			},
		},
	}

	// Add the rule to pass along requests to the Service's proxy port.
	pathTytpe := netv1.PathTypeImplementationSpecific
	rules = append(rules, netv1.IngressRule{
		Host: ingressName,
		IngressRuleValue: netv1.IngressRuleValue{
			HTTP: &netv1.HTTPIngressRuleValue{
				Paths: []netv1.HTTPIngressPath{
					{
						PathType: &pathTytpe,
						Backend:  *backend, // service backend, not the default backend
					},
				},
			},
		},
	})

	annotations := map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size":       "4096m",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":    "172800", // Insane, but might cut down on support requests
		"nginx.ingress.kubernetes.io/proxy-send-timeout":    "172800", // Also insane.
		"nginx.ingress.kubernetes.io/proxy-connect-timeout": "5000",   // Slightly less insane.
		"nginx.ingress.kubernetes.io/server-snippets": `location / {
	proxy_set_header Upgrade $http_upgrade;
	proxy_http_version 1.1;
	proxy_set_header X-Forwarded-Host $http_host;
	proxy_set_header X-Forwarded-Proto $scheme;
	proxy_set_header X-Forwarded-For $remote_addr;
	proxy_set_header Host $host;
	proxy_set_header Connection "upgrade";
	proxy_cache_bypass $http_upgrade;
}`,
	}

	return &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        job.InvocationID,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: netv1.IngressSpec{
			DefaultBackend:   defaultBackend, // default backend, not the service backend
			IngressClassName: &class,
			Rules:            rules,
		},
	}, nil
}
