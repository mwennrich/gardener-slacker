FROM golang:1.18-alpine as builder
RUN apk add make binutils
COPY / /work
WORKDIR /work
RUN make

FROM alpine:3.16
COPY --from=builder /work/bin/gardener-slacker /gardener-slacker
USER root
ENTRYPOINT ["/gardener-slacker","check"]
