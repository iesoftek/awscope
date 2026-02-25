package cost

import "strings"

var estimators = map[string]Estimator{}

func register(resourceType string, e Estimator) {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" || e == nil {
		return
	}
	estimators[resourceType] = e
}

func registryEstimator(resourceType string) Estimator {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return nil
	}
	return estimators[resourceType]
}

func init() {
	register("ec2:instance", ec2InstanceEstimator{})
	register("ec2:volume", ebsVolumeEstimator{})
	register("rds:db-instance", rdsInstanceEstimator{})
	register("rds:db-cluster", auroraClusterEstimator{})
	register("elbv2:load-balancer", elbv2LoadBalancerEstimator{})
	register("ecs:service", ecsServiceEstimator{})
	register("logs:log-group", logsLogGroupEstimator{})
	register("kms:key", kmsKeyEstimator{})
	register("secretsmanager:secret", secretsManagerSecretEstimator{})
	register("efs:file-system", efsFileSystemEstimator{})
}
