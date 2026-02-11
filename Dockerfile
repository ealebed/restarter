FROM golang:1.26 as build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/restarter ./cmd/restarter

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/restarter /restarter

USER 65532:65532

ENTRYPOINT ["/restarter"]
