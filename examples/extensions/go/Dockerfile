FROM golang:1.26.3-alpine AS build
WORKDIR /src
COPY extension.go gebext.go .
RUN go build -o /out/geblang-go-extension extension.go gebext.go

FROM alpine:3.21
COPY --from=build /out/geblang-go-extension /usr/local/bin/geblang-go-extension
CMD ["geblang-go-extension"]
