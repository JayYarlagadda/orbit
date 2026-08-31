#include "orbit_acquisition.h"

#include <errno.h>
#include <string.h>

#include <zephyr/logging/log.h>

LOG_MODULE_DECLARE(orbit_imu);

static int enqueue_sample(struct orbit_acq_ctx *ctx, const struct orbit_imu_sample *sample)
{
	if (ctx->count >= ORBIT_ACQ_QUEUE_DEPTH) {
		ctx->stats.dropped++;
		return -ENOSPC;
	}

	ctx->queue[ctx->tail] = *sample;
	ctx->tail = (ctx->tail + 1U) % ORBIT_ACQ_QUEUE_DEPTH;
	ctx->count++;
	ctx->stats.enqueued++;

	if (ctx->count > ctx->stats.high_water) {
		ctx->stats.high_water = ctx->count;
	}

	return 0;
}

int orbit_acq_init(struct orbit_acq_ctx *ctx, struct orbit_imu_data *imu)
{
	if (ctx == NULL || imu == NULL) {
		return -EINVAL;
	}

	memset(ctx, 0, sizeof(*ctx));
	ctx->imu = imu;
	k_mutex_init(&ctx->lock);
	return 0;
}

int orbit_acq_poll_once(struct orbit_acq_ctx *ctx)
{
	struct orbit_imu_sample sample;
	int ret;

	ret = orbit_imu_read_sample(ctx->imu, &sample);
	if (ret != 0) {
		return ret;
	}

	k_mutex_lock(&ctx->lock, K_FOREVER);
	ret = enqueue_sample(ctx, &sample);
	k_mutex_unlock(&ctx->lock);
	return ret;
}

int orbit_acq_on_data_ready(struct orbit_acq_ctx *ctx)
{
	struct orbit_imu_sample sample;
	int ret;

	ret = orbit_imu_read_sample(ctx->imu, &sample);
	if (ret != 0) {
		ctx->stats.missed_isr++;
		return ret;
	}

	k_mutex_lock(&ctx->lock, K_FOREVER);
	ret = enqueue_sample(ctx, &sample);
	k_mutex_unlock(&ctx->lock);
	return ret;
}

int orbit_acq_pop(struct orbit_acq_ctx *ctx, struct orbit_imu_sample *sample)
{
	if (sample == NULL) {
		return -EINVAL;
	}

	k_mutex_lock(&ctx->lock, K_FOREVER);
	if (ctx->count == 0U) {
		k_mutex_unlock(&ctx->lock);
		return -EAGAIN;
	}

	*sample = ctx->queue[ctx->head];
	ctx->head = (ctx->head + 1U) % ORBIT_ACQ_QUEUE_DEPTH;
	ctx->count--;
	ctx->stats.consumed++;
	k_mutex_unlock(&ctx->lock);
	return 0;
}

void orbit_acq_get_stats(const struct orbit_acq_ctx *ctx, struct orbit_acq_stats *out)
{
	if (out == NULL) {
		return;
	}

	*out = ctx->stats;
}
