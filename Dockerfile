FROM golang:alpine AS builder
WORKDIR /opt/orp
COPY . .
RUN go build -buildvcs=false
FROM alpine
WORKDIR /opt/orp
COPY --from=builder /opt/orp/opcua-reverse-proxy /opt/orp/opcua-reverse-proxy.json ./
EXPOSE 4840
ENTRYPOINT [ "./opcua-reverse-proxy" ]
