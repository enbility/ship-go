package api

type DeviceCategoryType uint

const (
	// Grid Connection Point Hub (GCPH) (e.g. a control unit from the public grid operator)
	DeviceCategoryTypeGridConnectionHub DeviceCategoryType = 1
	// Energy Management System (EMS) (device managing the electrical energy consumption/production of connected devices in the building)
	DeviceCategoryTypeEnergyManagementSystem DeviceCategoryType = 2
	// E-mobility related device (e.g., charging station)
	DeviceCategoryTypeEMobility DeviceCategoryType = 3
	// HVAC related device/system (e.g., heat pump)
	DeviceCategoryTypeHVAC DeviceCategoryType = 4
	// Inverter (PV/battery/hybrid inverter)
	DeviceCategoryTypeInverter DeviceCategoryType = 5
	// Domestic appliance (e.g., washing machine, dryer, fridge, etc.)
	DeviceCategoryTypeDomesticAppliance DeviceCategoryType = 6
	// Metering device (e.g., smart meter or sub-meter with its own communications technology)
	DeviceCategoryTypeMetering DeviceCategoryType = 7
)
