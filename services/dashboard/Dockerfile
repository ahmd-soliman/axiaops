FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

# EXPO_PUBLIC_* vars are baked into the static bundle at build time.
# Pass them as build args: docker build --build-arg EXPO_PUBLIC_KINDE_ISSUER=...
ARG EXPO_PUBLIC_KINDE_ISSUER
ARG EXPO_PUBLIC_KINDE_CLIENT_ID
ARG EXPO_PUBLIC_API_URL=/api
ENV EXPO_PUBLIC_KINDE_ISSUER=$EXPO_PUBLIC_KINDE_ISSUER
ENV EXPO_PUBLIC_KINDE_CLIENT_ID=$EXPO_PUBLIC_KINDE_CLIENT_ID
ENV EXPO_PUBLIC_API_URL=$EXPO_PUBLIC_API_URL

RUN npx expo export --platform web

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
