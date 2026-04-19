FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

ARG VITE_KINDE_ISSUER
ARG VITE_KINDE_CLIENT_ID
ARG VITE_API_URL=/api
ARG VITE_DEV_MODE=false
ENV VITE_KINDE_ISSUER=$VITE_KINDE_ISSUER
ENV VITE_KINDE_CLIENT_ID=$VITE_KINDE_CLIENT_ID
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_DEV_MODE=$VITE_DEV_MODE

RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY inject-env.sh /docker-entrypoint.d/40-inject-env.sh
RUN chmod +x /docker-entrypoint.d/40-inject-env.sh
EXPOSE 80
