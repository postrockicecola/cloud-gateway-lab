PROJECT := cloud-gateway-lab
CLUSTER := gateway-lab
NS      := gateway-lab

.PHONY: test images cluster load deploy status port-forward clean \
	k8s-up k8s-status k8s-deploy k8s-delete \
	scheduling-demo scheduling-taint scheduling-cleanup \
	gpu-check gpu-deploy gpu-cleanup \
	fault-pending fault-image fault-crash fault-readiness fault-gpu fault-cleanup \
	hpa-setup hpa-apply load-test

test:
	go test ./...
	go vet ./...
	kubectl kustomize deploy/k8s/base >/dev/null
	kubectl kustomize k8s >/dev/null

images:
	docker build --build-arg APP=ai-gateway -t $(PROJECT)/ai-gateway:dev .
	docker build --build-arg APP=mockprovider -t $(PROJECT)/mockprovider:dev .

# --- Kubernetes lab ---

k8s-up:
	@if kind get clusters 2>/dev/null | grep -qx $(CLUSTER); then \
		echo "kind cluster $(CLUSTER) already exists"; \
		echo "Want a fresh 3-node cluster?  make k8s-delete && make k8s-up"; \
	else \
		kind create cluster --name $(CLUSTER) --config deploy/kind/cluster.yaml; \
	fi

k8s-status:
	@echo "==> Nodes"
	kubectl get nodes -o wide
	@echo
	@echo "==> Deployment / ReplicaSet / Pod / Service / EndpointSlice / DaemonSet"
	kubectl get deploy,rs,pods,svc,endpointslice,ds -n $(NS) -o wide
	@echo
	@echo "DNS is in-cluster only. From a Pod: http://model:8080  http://mock-a:8080  http://redis:6379"

k8s-deploy: images
	docker pull redis:7-alpine
	docker pull alpine:3.20
	docker pull registry.k8s.io/pause:3.10
	kind load docker-image $(PROJECT)/ai-gateway:dev --name $(CLUSTER)
	kind load docker-image $(PROJECT)/mockprovider:dev --name $(CLUSTER)
	kind load docker-image redis:7-alpine --name $(CLUSTER)
	kind load docker-image alpine:3.20 --name $(CLUSTER)
	kind load docker-image registry.k8s.io/pause:3.10 --name $(CLUSTER)
	kubectl apply -k k8s
	kubectl rollout status deployment/redis -n $(NS)
	kubectl rollout status deployment/mock-a -n $(NS)
	kubectl rollout status deployment/mock-b -n $(NS)
	kubectl rollout status deployment/model -n $(NS)
	kubectl rollout status deployment/gateway -n $(NS)
	kubectl rollout status daemonset/node-agent -n $(NS)

k8s-delete:
	kind delete cluster --name $(CLUSTER)

# backward-compatible aliases
cluster: k8s-up
load:
	docker pull redis:7-alpine
	docker pull alpine:3.20
	kind load docker-image $(PROJECT)/ai-gateway:dev --name $(CLUSTER)
	kind load docker-image $(PROJECT)/mockprovider:dev --name $(CLUSTER)
	kind load docker-image redis:7-alpine --name $(CLUSTER)
	kind load docker-image alpine:3.20 --name $(CLUSTER)
deploy:
	kubectl apply -k k8s
	kubectl rollout status deployment/redis -n $(NS)
	kubectl rollout status deployment/mock-a -n $(NS)
	kubectl rollout status deployment/mock-b -n $(NS)
	kubectl rollout status deployment/model -n $(NS)
	kubectl rollout status deployment/gateway -n $(NS)
status: k8s-status
clean: k8s-delete

port-forward:
	kubectl port-forward -n $(NS) service/gateway 8080:8080

# --- Scheduler ---

scheduling-demo:
	bash scripts/scheduling-demo.sh

scheduling-taint:
	bash scripts/scheduling-taint.sh

scheduling-cleanup:
	bash scripts/scheduling-cleanup.sh

# --- GPU (optional; never required by k8s-up / k8s-deploy) ---

gpu-check:
	bash scripts/gpu-check.sh

gpu-deploy:
	bash scripts/gpu-deploy.sh

gpu-cleanup:
	bash scripts/gpu-cleanup.sh

# --- Faults ---

fault-pending:
	bash scripts/fault.sh pending

fault-image:
	bash scripts/fault.sh image

fault-crash:
	bash scripts/fault.sh crash

fault-readiness:
	bash scripts/fault.sh readiness

fault-gpu:
	bash scripts/fault.sh gpu

fault-cleanup:
	bash scripts/fault.sh cleanup

# --- HPA / load ---

hpa-setup:
	kubectl apply -k k8s/metrics-server
	kubectl rollout status deployment/metrics-server -n kube-system --timeout=180s

hpa-apply:
	kubectl apply -f k8s/gateway/hpa.yaml
	kubectl get hpa -n $(NS)

load-test:
	bash scripts/load-test.sh
