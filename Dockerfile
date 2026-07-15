FROM node:26-alpine AS webbuild
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=webbuild /web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nabu ./cmd/nabu

FROM alpine:3.24
RUN adduser -D -H nabu
USER nabu
COPY --from=build /out/nabu /usr/local/bin/nabu
EXPOSE 8080
ENTRYPOINT ["nabu"]
CMD ["--mode=api"]
