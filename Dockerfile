FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /monitor .

FROM scratch
COPY --from=build /monitor /monitor
USER 65532:65532
ENTRYPOINT ["/monitor"]
CMD ["-out", "/data"]
