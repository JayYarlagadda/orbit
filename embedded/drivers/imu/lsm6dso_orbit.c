/*
 * Register-level SPI driver for ST LSM6DSO (accelerometer + gyroscope).
 *
 * Demonstrates init, register access, reset/recovery, and data-ready
 * interrupt configuration without relying on Zephyr's in-tree sensor API.
 */

#include "lsm6dso_orbit.h"

#include <string.h>

#include <zephyr/kernel.h>
#include <zephyr/logging/log.h>
#include <zephyr/sys/byteorder.h>

LOG_MODULE_REGISTER(orbit_imu, CONFIG_ORBIT_IMU_LOG_LEVEL);

#define LSM6DSO_REG_WHO_AM_I   0x0FU
#define LSM6DSO_REG_CTRL1_XL     0x10U
#define LSM6DSO_REG_CTRL2_G      0x11U
#define LSM6DSO_REG_CTRL3_C      0x12U
#define LSM6DSO_REG_INT1_CTRL    0x0DU
#define LSM6DSO_REG_STATUS_REG   0x1EU
#define LSM6DSO_REG_OUTX_L_G     0x22U

#define LSM6DSO_SPI_READ_BIT     0x80U

/* 833 Hz ODR — nearest standard rate above the 500 Hz E1 target. */
#define LSM6DSO_ODR_833HZ        0x70U
#define LSM6DSO_XL_FS_4G         0x08U
#define LSM6DSO_G_FS_2000DPS     0x0CU

#define LSM6DSO_RESET_TIMEOUT_MS 50U
#define LSM6DSO_SPI_TIMEOUT_MS   10U

static int reg_read(const struct orbit_imu_config *cfg, uint8_t reg, uint8_t *value)
{
	uint8_t tx[2] = { reg | LSM6DSO_SPI_READ_BIT, 0U };
	uint8_t rx[2];
	const struct spi_buf tx_buf = { .buf = tx, .len = sizeof(tx) };
	const struct spi_buf rx_buf = { .buf = rx, .len = sizeof(rx) };
	const struct spi_buf_set tx_set = { .buffers = &tx_buf, .count = 1 };
	const struct spi_buf_set rx_set = { .buffers = &rx_buf, .count = 1 };

	if (spi_transceive_dt(&cfg->bus, &tx_set, &rx_set) != 0) {
		return -EIO;
	}

	*value = rx[1];
	return 0;
}

static int reg_write(const struct orbit_imu_config *cfg, uint8_t reg, uint8_t value)
{
	uint8_t tx[2] = { reg & ~LSM6DSO_SPI_READ_BIT, value };
	const struct spi_buf tx_buf = { .buf = tx, .len = sizeof(tx) };
	const struct spi_buf_set tx_set = { .buffers = &tx_buf, .count = 1 };

	if (spi_write_dt(&cfg->bus, &tx_set) != 0) {
		return -EIO;
	}

	return 0;
}

static int burst_read(const struct orbit_imu_config *cfg, uint8_t start_reg, uint8_t *buf, size_t len)
{
	uint8_t tx[13] = { start_reg | LSM6DSO_SPI_READ_BIT };
	uint8_t rx[13];
	const struct spi_buf tx_buf = { .buf = tx, .len = len + 1U };
	const struct spi_buf rx_buf = { .buf = rx, .len = len + 1U };
	const struct spi_buf_set tx_set = { .buffers = &tx_buf, .count = 1 };
	const struct spi_buf_set rx_set = { .buffers = &rx_buf, .count = 1 };

	if (spi_transceive_dt(&cfg->bus, &tx_set, &rx_set) != 0) {
		return -EIO;
	}

	memcpy(buf, &rx[1], len);
	return 0;
}

static int verify_who_am_i(const struct orbit_imu_config *cfg)
{
	uint8_t who = 0U;

	if (reg_read(cfg, LSM6DSO_REG_WHO_AM_I, &who) != 0) {
		return -EIO;
	}

	if (who != ORBIT_IMU_WHO_AM_I_EXPECTED) {
		LOG_ERR("WHO_AM_I mismatch: got 0x%02x, expected 0x%02x", who,
			ORBIT_IMU_WHO_AM_I_EXPECTED);
		return -ENODEV;
	}

	return 0;
}

static void imu_gpio_callback(const struct device *port, struct gpio_callback *cb, uint32_t pins)
{
	ARG_UNUSED(port);
	ARG_UNUSED(pins);

	struct orbit_imu_data *data = CONTAINER_OF(cb, struct orbit_imu_data, int_cb);

	k_sem_give(&data->data_ready);
}

int orbit_imu_init(const struct orbit_imu_config *cfg, struct orbit_imu_data *data)
{
	if (cfg == NULL || data == NULL) {
		return -EINVAL;
	}

	if (!spi_is_ready_dt(&cfg->bus)) {
		LOG_ERR("SPI bus not ready");
		return -ENODEV;
	}

	memset(data, 0, sizeof(*data));
	data->cfg = cfg;
	k_sem_init(&data->data_ready, 0, 1);

	if (orbit_imu_reset(data) != 0) {
		return -EIO;
	}

	if (verify_who_am_i(cfg) != 0) {
		return -ENODEV;
	}

	if (orbit_imu_configure(data) != 0) {
		return -EIO;
	}

	data->initialized = true;
	LOG_INF("LSM6DSO initialized (WHO_AM_I ok)");
	return 0;
}

int orbit_imu_reset(struct orbit_imu_data *data)
{
	const struct orbit_imu_config *cfg = data->cfg;

	/* SW_RESET in CTRL3_C (bit 0). */
	if (reg_write(cfg, LSM6DSO_REG_CTRL3_C, BIT(0)) != 0) {
		return -EIO;
	}

	k_msleep(LSM6DSO_RESET_TIMEOUT_MS);
	data->reset_count++;

	return verify_who_am_i(cfg);
}

int orbit_imu_configure(struct orbit_imu_data *data)
{
	const struct orbit_imu_config *cfg = data->cfg;

	/* BDU + IF_INC: block updates, auto-increment register address. */
	if (reg_write(cfg, LSM6DSO_REG_CTRL3_C, BIT(6) | BIT(2)) != 0) {
		return -EIO;
	}

	/* Accelerometer: 833 Hz, ±4 g. */
	if (reg_write(cfg, LSM6DSO_REG_CTRL1_XL, LSM6DSO_ODR_833HZ | LSM6DSO_XL_FS_4G) != 0) {
		return -EIO;
	}

	/* Gyroscope: 833 Hz, ±2000 dps. */
	if (reg_write(cfg, LSM6DSO_REG_CTRL2_G, LSM6DSO_ODR_833HZ | LSM6DSO_G_FS_2000DPS) != 0) {
		return -EIO;
	}

	/* Route data-ready to INT1. */
	if (reg_write(cfg, LSM6DSO_REG_INT1_CTRL, BIT(0)) != 0) {
		return -EIO;
	}

	return 0;
}

int orbit_imu_read_sample(struct orbit_imu_data *data, struct orbit_imu_sample *sample)
{
	const struct orbit_imu_config *cfg = data->cfg;
	uint8_t raw[12];
	int ret;

	if (!data->initialized || sample == NULL) {
		return -EINVAL;
	}

	ret = burst_read(cfg, LSM6DSO_REG_OUTX_L_G, raw, sizeof(raw));
	if (ret != 0) {
		data->timeout_count++;
		return ret;
	}

	sample->gyro[0] = (int16_t)sys_get_le16(&raw[0]);
	sample->gyro[1] = (int16_t)sys_get_le16(&raw[2]);
	sample->gyro[2] = (int16_t)sys_get_le16(&raw[4]);
	sample->accel[0] = (int16_t)sys_get_le16(&raw[6]);
	sample->accel[1] = (int16_t)sys_get_le16(&raw[8]);
	sample->accel[2] = (int16_t)sys_get_le16(&raw[10]);
	sample->timestamp_us = k_cyc_to_us_near64(k_uptime_ticks());
	sample->sequence++;

	return 0;
}

int orbit_imu_wait_data_ready(struct orbit_imu_data *data, k_timeout_t timeout)
{
	if (k_sem_take(&data->data_ready, timeout) != 0) {
		data->timeout_count++;
		return -ETIMEDOUT;
	}

	return 0;
}

int orbit_imu_enable_data_ready_irq(struct orbit_imu_data *data)
{
	const struct orbit_imu_config *cfg = data->cfg;
	int ret;

	if (!gpio_is_ready_dt(&cfg->int_gpio)) {
		LOG_ERR("INT GPIO not ready");
		return -ENODEV;
	}

	ret = gpio_pin_configure_dt(&cfg->int_gpio, GPIO_INPUT);
	if (ret != 0) {
		return ret;
	}

	ret = gpio_pin_interrupt_configure_dt(&cfg->int_gpio, GPIO_INT_EDGE_TO_ACTIVE);
	if (ret != 0) {
		return ret;
	}

	gpio_init_callback(&data->int_cb, imu_gpio_callback, BIT(cfg->int_gpio.pin));
	ret = gpio_add_callback(cfg->int_gpio.port, &data->int_cb);
	if (ret != 0) {
		return ret;
	}

	LOG_INF("data-ready interrupt enabled on pin %u", cfg->int_gpio.pin);
	return 0;
}
