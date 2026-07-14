FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nabu ./cmd/nabu

FROM alpine:3.22
RUN adduser -D -H nabu
USER nabu
COPY --from=build /out/nabu /usr/local/bin/nabu
EXPOSE 8080
ENTRYPOINT ["nabu"]
CMD ["--mode=api"]
