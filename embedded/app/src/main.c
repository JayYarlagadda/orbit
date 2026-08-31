/*
 * E1 demo: polling baseline, then data-ready ISR → SPI read → bounded queue.
 *
 * Target rate: 833 Hz hardware ODR (nearest standard rate to 500 Hz goal).
 * Prints per-second stats over UART for bench verification.
 */

#include <stdio.h>

#include <zephyr/device.h>
#include <zephyr/drivers/gpio.h>
#include <zephyr/drivers/spi.h>
#include <zephyr/kernel.h>
#include <zephyr/logging/log.h>

#include "orbit_acquisition.h"
#include "lsm6dso_orbit.h"

LOG_MODULE_REGISTER(orbit_e1, LOG_LEVEL_INF);

#define IMU_NODE DT_CHOSEN(orbit_imu)

#if !DT_NODE_EXISTS(IMU_NODE)
#error "Devicetree chosen node orbit,imu is required"
#endif

static const struct orbit_imu_config imu_cfg = {
	.bus = SPI_DT_SPEC_GET(IMU_NODE,
			       SPI_OP_MODE_MASTER | SPI_WORD_SET(8) | SPI_MODE_CPOL | SPI_MODE_CPHA,
			       0),
	.int_gpio = GPIO_DT_SPEC_GET(IMU_NODE, int_gpios),
};

static struct orbit_imu_data imu_data;
static struct orbit_acq_ctx acq_ctx;

/* 833 Hz ODR → ~1201 µs period; 2 ms timeout gives margin. */
#define DATA_READY_TIMEOUT K_USEC(2000)

static void print_stats(const char *phase, uint32_t elapsed_sec, const struct orbit_acq_stats *stats)
{
	printk("[%s +%us] enq=%u drop=%u cons=%u hwm=%u missed=%u\r\n",
	       phase, elapsed_sec, stats->enqueued, stats->dropped, stats->consumed,
	       stats->high_water, stats->missed_isr);
}

static void run_polling_phase(void)
{
	const uint32_t duration_sec = CONFIG_ORBIT_E1_POLL_DURATION_SEC;
	uint32_t last_report = 0U;

	printk("E1 polling baseline (%u s)\r\n", duration_sec);

	for (uint32_t elapsed = 0U; elapsed < duration_sec; elapsed++) {
		uint32_t target_samples = (elapsed + 1U) * 833U;
		uint32_t got = 0U;

		while (got < target_samples) {
			if (orbit_acq_poll_once(&acq_ctx) == 0) {
				got++;
			}
		}

		if (elapsed != last_report) {
			struct orbit_acq_stats stats;

			orbit_acq_get_stats(&acq_ctx, &stats);
			print_stats("poll", elapsed + 1U, &stats);
			last_report = elapsed + 1U;
		}
	}
}

static void acquisition_thread(void *p1, void *p2, void *p3)
{
	ARG_UNUSED(p1);
	ARG_UNUSED(p2);
	ARG_UNUSED(p3);

	const uint32_t duration_sec = CONFIG_ORBIT_E1_ISR_DURATION_SEC;
	uint32_t last_report = 0U;

	printk("E1 ISR acquisition (%u s)\r\n", duration_sec);

	for (uint32_t elapsed = 0U; elapsed < duration_sec; elapsed++) {
		uint32_t target_samples = (elapsed + 1U) * 833U;
		uint32_t got = 0U;

		while (got < target_samples) {
			if (orbit_imu_wait_data_ready(&imu_data, DATA_READY_TIMEOUT) != 0) {
				continue;
			}

			if (orbit_acq_on_data_ready(&acq_ctx) == 0) {
				got++;
			}
		}

		if (elapsed != last_report) {
			struct orbit_acq_stats stats;

			orbit_acq_get_stats(&acq_ctx, &stats);
			print_stats("isr", elapsed + 1U, &stats);
			last_report = elapsed + 1U;
		}
	}
}

K_THREAD_STACK_DEFINE(acq_stack, 2048);
static struct k_thread acq_thread;

int main(void)
{
	int ret;

	printk("Orbit E1 — Nucleo-H743ZI + LSM6DSO\r\n");

	ret = orbit_imu_init(&imu_cfg, &imu_data);
	if (ret != 0) {
		LOG_ERR("IMU init failed: %d", ret);
		return ret;
	}

	ret = orbit_acq_init(&acq_ctx, &imu_data);
	if (ret != 0) {
		LOG_ERR("acquisition init failed: %d", ret);
		return ret;
	}

	run_polling_phase();

	ret = orbit_imu_enable_data_ready_irq(&imu_data);
	if (ret != 0) {
		LOG_ERR("IRQ enable failed: %d", ret);
		return ret;
	}

	k_thread_create(&acq_thread, acq_stack, K_THREAD_STACK_SIZEOF(acq_stack),
			acquisition_thread, NULL, NULL, NULL, K_PRIO_PREEMPT(5), 0,
			K_NO_WAIT);
	k_thread_name_set(&acq_thread, "acquisition");

	k_thread_join(&acq_thread, K_FOREVER);

	struct orbit_acq_stats final_stats;

	orbit_acq_get_stats(&acq_ctx, &final_stats);
	printk("E1 complete: enq=%u drop=%u imu_timeouts=%u resets=%u\r\n",
	       final_stats.enqueued, final_stats.dropped, imu_data.timeout_count,
	       imu_data.reset_count);

	return 0;
}
