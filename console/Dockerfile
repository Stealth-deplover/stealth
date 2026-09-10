# Build from the repository root:
# docker build -f console/Dockerfile -t stealth-console .
FROM node:26-alpine AS dependencies
WORKDIR /app/console
COPY console/package.json console/package-lock.json ./
RUN npm ci
COPY console/tools/openapi-codegen/package.json console/tools/openapi-codegen/package-lock.json ./tools/openapi-codegen/
RUN npm --prefix tools/openapi-codegen ci --ignore-scripts
COPY console/tools/eslint/package.json console/tools/eslint/package-lock.json ./tools/eslint/
RUN npm --prefix tools/eslint ci --ignore-scripts

FROM dependencies AS builder
WORKDIR /app/console
COPY console/ ./
COPY openapi/ /app/openapi/
ARG NEXT_PUBLIC_API_BASE_URL=
ENV NEXT_PUBLIC_API_BASE_URL=${NEXT_PUBLIC_API_BASE_URL}
RUN mkdir -p public && npm run build

FROM node:26-alpine AS runner
ENV NODE_ENV=production
ENV HOSTNAME=0.0.0.0
WORKDIR /app
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown
LABEL org.opencontainers.image.title="Stealth Console" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT_SHA" \
      org.opencontainers.image.created="$BUILD_TIME" \
      org.opencontainers.image.source="https://github.com/Stealth-deplover/stealth"
RUN addgroup -S nodejs -g 1001 && adduser -S nextjs -u 1001 -G nodejs
COPY --from=builder --chown=nextjs:nodejs /app/console/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/console/.next/static ./.next/static
COPY --from=builder --chown=nextjs:nodejs /app/console/public ./public
USER nextjs
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD node -e "fetch('http://127.0.0.1:3000').then((response) => process.exit(response.ok ? 0 : 1)).catch(() => process.exit(1))"
CMD ["node", "server.js"]
