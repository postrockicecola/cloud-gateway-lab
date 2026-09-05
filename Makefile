PROJECT := cloud-gateway-lab
CLUSTER := gateway-lab

.PHONY: test images cluster load deploy status port-forward clean

test:
	go test ./...
	go vet ./...
	kubectl kustomize deploy/k8s/base >/dev/null

images:
	docker build --build-arg APP=gateway -t $(PROJECT)/gateway:dev .
	docker build --build-arg APP=backend -t $(PROJECT)/backend:dev .

cluster:
	kind create cluster --name $(CLUSTER)

load:
	docker pull redis:7-alpine
	kind load docker-image $(PROJECT)/gateway:dev --name $(CLUSTER)
	kind load docker-image $(PROJECT)/backend:dev --name $(CLUSTER)
	kind load docker-image redis:7-alpine --name $(CLUSTER)

deploy:
	kubectl apply -k deploy/k8s/base
	kubectl rollout status deployment/redis -n gateway-lab
	kubectl rollout status deployment/gateway -n gateway-lab
	kubectl rollout status deployment/users -n gateway-lab
	kubectl rollout status deployment/products -n gateway-lab

status:
	kubectl get pods,services -n gateway-lab -o wide

port-forward:
	kubectl port-forward -n gateway-lab service/gateway 8080:8080

clean:
	kind delete cluster --name $(CLUSTER)
