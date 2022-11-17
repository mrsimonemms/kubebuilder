package resources

import (
	"fmt"

	certmanagerv1 "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	v1 "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	installerv1alpha1 "github.com/mrsimonemms/kubebuilder/api/v1alpha1"
)

const (
	nodeVolumeName      = "node-fs0"
	nodeVolumeMountPath = "/mnt/node0"
	tmpVolumeName       = "tmp-storage"
	tmpVolumeMouthPath  = "/tmp"
	tlsCertName         = "https-certificates"
)

func getLabels(clientResource *installerv1alpha1.Config) map[string]string {
	return map[string]string{
		"app":       "gitpod",
		"component": "operator",
	}
}

func CreateCertificate(clientResource *installerv1alpha1.Config) *certmanagerv1.Certificate {
	return &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tlsCertName,
			Namespace: clientResource.Namespace,
			Labels:    getLabels(clientResource),
		},
		Spec: certmanagerv1.CertificateSpec{
			SecretName: tlsCertName,
			IssuerRef: v1.ObjectReference{
				Name: "gitpod-issuer",
				Kind: "ClusterIssuer",
			},
			DNSNames: []string{
				clientResource.Spec.InstallerConfig.Domain,
				fmt.Sprintf("*.%s", clientResource.Spec.InstallerConfig.Domain),
				fmt.Sprintf("*.ws.%s", clientResource.Spec.InstallerConfig.Domain),
			},
		},
	}
}

func CreateConfigMap(clientResource *installerv1alpha1.Config) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientResource.Name,
			Namespace: clientResource.Namespace,
			Labels:    getLabels(clientResource),
		},
		Data: map[string]string{
			// KOTS settings
			"GITPOD_INSTALLER_CONFIG": fmt.Sprintf("%s/gitpod-config.yaml", tmpVolumeMouthPath),
			"GITPOD_OBJECTS":          fmt.Sprintf("%s/gitpod", tmpVolumeMouthPath),

			// General settings
			"DOMAIN":    clientResource.Spec.InstallerConfig.Domain,
			"NAMESPACE": clientResource.Namespace,

			// Secret names

			// Database settings
			"DB_INCLUSTER_ENABLED": "1",

			// Airgap settings
			"HAS_LOCAL_REGISTRY": "0",

			// Registry settings
			"REGISTRY_INCLUSTER_ENABLED": "1",

			// Storage settings
			"STORE_PROVIDER": "incluster",

			// TLS certificate settings
			"CERT_MANAGER_ENABLED":    "1",
			"TLS_SELF_SIGNED_ENABLED": "0",

			// User management settings
			"USER_MANAGEMENT_BLOCK_ENABLED": "0",

			// Advanced settings
			"ADVANCED_MODE_ENABLED": "0",

			"CUSTOMIZATION_PATCH_ENABLED": "0",

			// Customizations
			"CONFIG_PATCH":        "",
			"CUSTOMIZATION_PATCH": "",
		},
	}
}

func CreateResources(clientResource *installerv1alpha1.Config) []client.Object {
	return []client.Object{
		CreateConfigMap(clientResource),
		CreateInstallerJob(clientResource),
	}
}

func CreateInstallerJob(clientResource *installerv1alpha1.Config) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientResource.Name,
			Namespace: clientResource.Namespace,
			Labels:    getLabels(clientResource),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            pointer.Int32(1),
			TTLSecondsAfterFinished: pointer.Int32(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: getLabels(clientResource),
				},
				Spec: corev1.PodSpec{
					// ServiceAccountName: "",
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:  "installer",
							Image: clientResource.Spec.InstallerImage,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      nodeVolumeName,
									MountPath: nodeVolumeMountPath,
									ReadOnly:  true,
								},
								{
									Name:      tmpVolumeName,
									MountPath: tmpVolumeMouthPath,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "MOUNT_PATH",
									Value: nodeVolumeMountPath,
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: clientResource.Name,
										},
									},
								},
							},
							Command: []string{
								"/app/scripts/kots-install.sh",
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: nodeVolumeName,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/",
									Type: func() *corev1.HostPathType {
										r := corev1.HostPathDirectory
										return &r
									}(),
								},
							},
						},
						{
							Name: tmpVolumeName,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

func CreatePod(clientResource *installerv1alpha1.Config) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: clientResource.Namespace,
			Labels:    getLabels(clientResource),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "gitpod-installer", // @todo(sje): do we need some additional things in here?
					Image: clientResource.Spec.InstallerImage,
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}
}
