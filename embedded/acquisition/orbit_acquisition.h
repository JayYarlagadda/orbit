#ifndef ORBIT_ACQUISITION_ORBIT_ACQUISITION_H_
#define ORBIT_ACQUISITION_ORBIT_ACQUISITION_H_

#include <stdbool.h>
#include <stdint.h>

#include <zephyr/kernel.h>

#include "lsm6dso_orbit.h"

#ifdef __cplusplus
extern "C" {
#endif

#define ORBIT_ACQ_QUEUE_DEPTH CONFIG_ORBIT_ACQ_QUEUE_DEPTH

struct orbit_acq_stats {
	uint32_t enqueued;
	uint32_t dropped;
	uint32_t consumed;
	uint32_t missed_isr;
	uint32_t high_water;
};

struct orbit_acq_ctx {
	struct orbit_imu_data *imu;
	struct orbit_imu_sample queue[ORBIT_ACQ_QUEUE_DEPTH];
	uint16_t head;
	uint16_t tail;
	uint16_t count;
	struct orbit_acq_stats stats;
	struct k_mutex lock;
};

int orbit_acq_init(struct orbit_acq_ctx *ctx, struct orbit_imu_data *imu);

int orbit_acq_poll_once(struct orbit_acq_ctx *ctx);

int orbit_acq_on_data_ready(struct orbit_acq_ctx *ctx);

int orbit_acq_pop(struct orbit_acq_ctx *ctx, struct orbit_imu_sample *sample);

void orbit_acq_get_stats(const struct orbit_acq_ctx *ctx, struct orbit_acq_stats *out);

#ifdef __cplusplus
}
#endif

#endif /* ORBIT_ACQUISITION_ORBIT_ACQUISITION_H_ */
