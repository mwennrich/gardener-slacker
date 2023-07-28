GO111MODULE := on
DOCKER_TAG := $(or ${GIT_TAG_NAME}, latest)

all: gardener-slacker

.PHONY: gardener-slacker
gardener-slacker:
	go build -o bin/gardener-slacker cmd/main.go
	strip bin/gardener-slacker

.PHONY: dockerimages
dockerimages:
	docker build -t mwennrich/gardener-slacker:${DOCKER_TAG} .

.PHONY: dockerpush
dockerpush:
	docker push mwennrich/gardener-slacker:${DOCKER_TAG}

.PHONY: clean
clean:
	rm -f bin/*
