FROM node@sha256:de170b39439eb0e5a3444ad7d60b015614a381e10fecae2e30ec9e735122f38d
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY server.mjs policy.mjs policy.test.mjs index.html ui.mjs ./
USER node
ENV DATA_DIR=/data
CMD ["node", "server.mjs"]
