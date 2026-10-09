FROM oven/bun:1.4.0 AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY index.html tsconfig.json vite.config.ts ./
COPY src ./src
RUN bun run build

FROM node:24.19.0-bookworm-slim
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY server ./server
USER node
ENV ANVIL_CONSOLE_HOST=0.0.0.0 PORT=8081
EXPOSE 8081
CMD ["node", "server/main.mjs"]
