FROM golang:1.18 as builder

ENV GO111MODULE=on
ENV CGO_ENABLED=0

COPY / /work
WORKDIR /work
RUN make

FROM scratch
COPY --from=builder /work/bin/gardener-slacker /gardener-slacker
USER 65535
ENTRYPOINT ["/gardener-slacker","check"]
