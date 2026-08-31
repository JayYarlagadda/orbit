#ifndef ORBIT_DRIVERS_IMU_LSM6DSO_ORBIT_H_
#define ORBIT_DRIVERS_IMU_LSM6DSO_ORBIT_H_

#include <stdbool.h>
#include <stdint.h>

#include <zephyr/device.h>
#include <zephyr/drivers/gpio.h>
#include <zephyr/drivers/spi.h>

#ifdef __cplusplus
extern "C" {
#endif

#define ORBIT_IMU_WHO_AM_I_EXPECTED 0x6CU

struct orbit_imu_sample {
	uint32_t sequence;
	uint32_t timestamp_us;
	int16_t gyro[3];
	int16_t accel[3];
};

struct orbit_imu_config {
	struct spi_dt_spec bus;
	struct gpio_dt_spec int_gpio;
};

struct orbit_imu_data {
	const struct orbit_imu_config *cfg;
	struct gpio_callback int_cb;
	struct k_sem data_ready;
	uint32_t timeout_count;
	uint32_t reset_count;
	bool initialized;
};

int orbit_imu_init(const struct orbit_imu_config *cfg, struct orbit_imu_data *data);

int orbit_imu_reset(struct orbit_imu_data *data);

int orbit_imu_configure(struct orbit_imu_data *data);

int orbit_imu_read_sample(struct orbit_imu_data *data, struct orbit_imu_sample *sample);

int orbit_imu_wait_data_ready(struct orbit_imu_data *data, k_timeout_t timeout);

int orbit_imu_enable_data_ready_irq(struct orbit_imu_data *data);

#ifdef __cplusplus
}
#endif

#endif /* ORBIT_DRIVERS_IMU_LSM6DSO_ORBIT_H_ */
