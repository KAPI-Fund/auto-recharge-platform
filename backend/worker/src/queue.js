import Redis from "ioredis";

export class TaskQueue {
  constructor({ redisUrl, queueName, workerId = "worker" }) {
    this.queueName = queueName;
    this.processingQueue = `${queueName}:processing:${String(workerId).replace(/[^a-zA-Z0-9:_-]/g, "_")}`;
    this.redis = new Redis(redisUrl, { maxRetriesPerRequest: null });
  }

  async next() {
    const raw = await this.redis.brpoplpush(this.queueName, this.processingQueue, 0);
    if (!raw) {
      return null;
    }
    try {
      const message = JSON.parse(raw);
      if (!message || typeof message !== "object") throw new Error("invalid task message");
      return { ...message, __queueRaw: raw };
    } catch {
      await this.redis.lrem(this.processingQueue, 1, raw);
      return null;
    }
  }

  async acknowledge(message) {
    const raw = typeof message === "string" ? message : message?.__queueRaw;
    if (raw) await this.redis.lrem(this.processingQueue, 1, raw);
  }

  async requeue(message) {
    const raw = typeof message === "string" ? message : message?.__queueRaw;
    if (!raw) return;
    await this.redis.lrem(this.processingQueue, 1, raw);
    await this.redis.rpush(this.queueName, raw);
  }

  async recover() {
    const stale = await this.redis.lrange(this.processingQueue, 0, -1);
    for (const raw of stale) {
      await this.redis.lrem(this.processingQueue, 1, raw);
      await this.redis.rpush(this.queueName, raw);
    }
    return stale.length;
  }

  async close() {
    await this.redis.quit();
  }
}
