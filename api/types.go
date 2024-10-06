package api

type DeviceCategory uint

const (
	// Grid Connection Point Hub (GCPH) (e.g. a control unit from the public grid operator)
	DeviceCategoryGridConnectionHub DeviceCategory = 1
	// Energy Management System (EMS) (device managing the electrical energy consumption/production of connected devices in the building)
	DeviceCategoryEnergyManagementSystem DeviceCategory = 2
	// E-mobility related device (e.g., charging station)
	DeviceCategoryEMobility DeviceCategory = 3
	// HVAC related device/system (e.g., heat pump)
	DeviceCategoryHVAC DeviceCategory = 4
	// Inverter (PV/battery/hybrid inverter)
	DeviceCategoryInverter DeviceCategory = 5
	// Domestic appliance (e.g., washing machine, dryer, fridge, etc.)
	DeviceCategoryDomesticAppliance DeviceCategory = 6
	// Metering device (e.g., smart meter or sub-meter with its own communications technology)
	DeviceCategoryMetering DeviceCategory = 7
)
