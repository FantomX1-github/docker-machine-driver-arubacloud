package main

import (
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/mcnflag"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/arubacloud/goarubacloud"
    "github.com/arubacloud/goarubacloud/models"
	"time"
	"fmt"
	"io/ioutil"
	"net"
	"github.com/docker/machine/libmachine/state"
	"github.com/docker/machine/libmachine/ssh"
	"path/filepath"
	"os"
)

const (
	statusTimeout = 200
)

type Driver struct {
	drivers.BaseDriver

	TemplateID    int
	TemplateName string
	Size string
	PackageID     int
	AdminPassword string
	Username      string
	Password      string
	Endpoint      string

	// internal ids
	ServerId      int
	ServerName    string
	KeyPairName   string
	
	Type   string
	IpAddress   string

	// internal
	client        *goarubacloud.API
}

const(
	defaultTemplate = "ubuntu1604_x64_1_0"
	defaultEndpoint = "dc1"
	defaultSize = "Large"
	machineType = "Smart"
)

// GetCreateFlags registers the "machine create" flags recognized by this driver, including
// their help text and defaults.
func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{
			EnvVar: "AC_USERNAME",
			Name: "ac_username",
			Usage: "ArubaCloud Userame",
			Value: "",
		},
		mcnflag.StringFlag{
			EnvVar: "AC_PASSWORD",
			Name: "ac_password",
			Usage: "ArubaCloud Password",
			Value: "",
		},
		mcnflag.StringFlag{
			EnvVar: "AC_ADMIN_PASSWORD",
			Name: "ac_admin_password",
			Usage: "Machine root password",
			Value: "",
		},
		mcnflag.StringFlag{
			EnvVar: "AC_ENDPOINT",
			Name: "ac_endpoint",
			Usage: "Endpoint name (dc1,dc2,dc3 etc.)",
			Value: defaultEndpoint,
		},
		mcnflag.StringFlag{
			EnvVar: "AC_TEMPLATE",
			Name: "ac_template",
			Usage: "VM Template",
			Value: defaultTemplate,
		},
		mcnflag.StringFlag{
			EnvVar: "AC_SIZE",
			Name: "ac_size",
			Usage: "Machine Size",
			Value: defaultSize,
		},
		mcnflag.StringFlag{
			EnvVar: "AC_TYPE",
			Name: "ac_type",
			Usage: "Machine Type",
			Value: machineType,
		},
		mcnflag.StringFlag{
			EnvVar: "AC_IP",
			Name: "ac_ip",
			Usage: "Set this to use an already purchased Ip Address",
			Value: "",
		},
	}
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return "arubacloud"
}

func (d *Driver) PreCreateCheck() error {
	client := d.getClient()
	
	hyperV := 4
	if (d.Type == "Pro") {
		hyperV = 2
	}
	
	log.Debug("Validating Template ", d.TemplateName)
	_, err := client.GetTemplate(d.TemplateName, hyperV)
	if err != nil {
		fmt.Println("GetTemplate: ", err)
		return err
	}


	// Use a common key or create a machine specific one
	if len(d.KeyPairName) != 0 {
		d.SSHKeyPath = filepath.Join(d.StorePath, "sshkeys", d.KeyPairName)
	} else {
		d.KeyPairName = fmt.Sprintf("%s-%s", d.MachineName, mcnutils.GenerateRandomID())
	}

	return nil
}

// getClient returns an ArubaCloud API client pointing to dc1
func (d *Driver) getClient() (api *goarubacloud.API) {
	if d.client == nil {
		client, err := goarubacloud.NewAPI(d.Endpoint, d.Username, d.Password)
		if err != nil {
			return nil
		}
		d.client = client
	}

	return d.client
}

func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	d.Username = flags.String("ac_username")
	d.Password = flags.String("ac_password")
	d.AdminPassword = flags.String("ac_admin_password")
	d.PackageID = flags.Int("ac_package_id")
	d.TemplateName = flags.String("ac_template")
	d.Size = flags.String("ac_size")
	d.Endpoint = flags.String("ac_endpoint")
	d.KeyPairName = flags.String("ac_ssh_key")
	d.Type = flags.String("ac_type")
	d.IPAddress = flags.String("ac_ip")
	
	d.SSHUser = "root"

	return nil
}

func (d *Driver) waitForServerStatus(status int) (server *models.Server, err error) {
	//func WaitForSpecificOrError(f func() (bool, error), maxAttempts int, waitInterval time.Duration) error
	return server, mcnutils.WaitForSpecificOrError(func() (bool, error) {
		server, err = d.client.GetServer(d.ServerId)
		if err != nil {
			return true, err
		}
		log.Debugf("Machine", map[string]interface{}{
			"Name":  d.ServerName,
			"State": server.ServerStatus,
		})

		if server.ServerStatus == 4 {
			return true, fmt.Errorf("Instance creation failed. Instance is in ERROR state")
		}

		if server.ServerStatus == status {
			return true, nil
		}
		return false, nil
	}, 10, 60 * time.Second)
}

func (d *Driver) CreateSmart() error {
	
	log.Debug("Create ", d.TemplateName)
	client := d.getClient()

	key, err := d.createKeyPair()
	if err != nil {
		return err
	}

	

	log.Debug("Get Template ", d.TemplateName)
	template, err := client.GetTemplate(d.TemplateName, 2)
	if err != nil {
		return err
	} else {
		log.Debug("Template found with Id: ", template.Id)
	}
	
	log.Debug("Get Package ", d.TemplateName)
	cloudpackage, err := client.GetPreconfiguredPackage(d.Size)
	if err != nil {
		return err
	} else {
		log.Debug("Package found with Id: ", cloudpackage.PackageID)
	}
	
	// Create instance
	log.Debug("Creating ArubaCloud server... with packageID: ", cloudpackage.PackageID)

	instance, err := client.CreateServerSmart(
		d.MachineName,
		d.AdminPassword,
		cloudpackage.PackageID,
		template.Id,
		key,
	)

	if err != nil {
		log.Debug(err)
		return err
	}

	log.Debug("Waiting for the server to be ready...")
	servers, err := client.GetServers()
	if err != nil {
		return err
	}

	// Retrieving ServerID from server list
	for _, server := range servers {
		log.Debugf("Iterating server name: %s", server.Name)
		if server.Name == d.MachineName {
			d.ServerId = server.ServerId
			log.Debugf("Setting Driver ServerId to: %d", d.ServerId)
		}
	}

	if d.ServerId == 0 {
		return fmt.Errorf("No Server found with Name: %s", d.MachineName)
	}

	// Retrieve ServerDetails for the given ServerID
	detailed_server_response, err := client.GetServer(d.ServerId)
	if err != nil {
		return err
	}

	// Override instance object with the new unmarshaled detailed server response
	instance = detailed_server_response

	// Wait until instance is ACTIVE
	log.Debugf("Waiting for ArubaCloud Server...", map[string]interface{}{"MachineID": d.ServerId})
	instance, err = d.waitForServerStatus(3)
	if err != nil {
		return err
	}

	// In order to obtain the IP address we have to get the server detail

	// Save Ip address that should be available at this point
	d.IPAddress = ""
	d.IPAddress = instance.EasyCloudIPAddress.Value

	if d.IPAddress == "" {
		return fmt.Errorf("No IP found for instance %s", instance.ServerId)
	}

	log.Debugf("IP address found", map[string]interface{}{
		"MachineID": d.ServerId,
		"IP":        d.IPAddress,
	})
	
	return nil
}

func (d *Driver) CreatePro() error {
	
	log.Debug("Create ", d.TemplateName)
	client := d.getClient()

	key, err := d.createKeyPair()
	if err != nil {
		return err
	}

	

	log.Debug("Get Template ", d.TemplateName)
	template, err := client.GetTemplate(d.TemplateName, 2)
	if err != nil {
		return err
	} else {
		log.Debug("Template found with Id: ", template.Id)
	}
	
	ipID := 0
	ipAddressValue := ""
	
	if len(d.IPAddress) > 0 {
		log.Debug("Get IpAddress ", d.IPAddress)
		ipAddress, err := client.GetPurchasedIpAddress(d.IPAddress)
		ipID = ipAddress.ResourceId
		ipAddressValue = d.IPAddress
		if err != nil {
			return err
		} else {
			log.Debug("IpAddress found with Id: ", ipAddress.ResourceId)
		}
	} else {
		log.Debug("Purchasing IpAddress ", d.IPAddress)
		ipAddress, err := client.PurchaseIpAddress()
		ipID = ipAddress.ResourceId
		ipAddressValue = ipAddress.Value
		if err != nil {
			return err
		} else {
			log.Debug("IpAddress purchased with Id: ", ipAddress.ResourceId)
		}
	}
	
	
	// Create instance
	log.Debug("Creating ArubaCloud server...")
	
	diskSize := 20
	cpuQuantity := 1
	ramQuantity := 1
	
	switch d.Size{
		case "Small":
			diskSize = 20
			cpuQuantity = 1
			ramQuantity = 1
		case "Medium":
			cpuQuantity = 1
			ramQuantity = 2
			diskSize = 40
		case "Large":
			cpuQuantity = 2
			ramQuantity = 4
			diskSize = 80
		case "Extra Large":
			cpuQuantity = 4
			ramQuantity = 8
			diskSize = 160
			
	}
	
	
	instance, err := client.CreateServerPro(
		d.MachineName,
		d.AdminPassword,
		template.Id,
		key,
		ipID,
		diskSize,
		cpuQuantity,
		ramQuantity,
	)

	if err != nil {
		log.Debug(err)
		return err
	}

	log.Debug("Waiting for the server to be ready...")
	servers, err := client.GetServers()
	if err != nil {
		return err
	}

	// Retrieving ServerID from server list
	for _, server := range servers {
		log.Debugf("Iterating server name: %s", server.Name)
		if server.Name == d.MachineName {
			d.ServerId = server.ServerId
			log.Debugf("Setting Driver ServerId to: %d", d.ServerId)
		}
	}

	if d.ServerId == 0 {
		return fmt.Errorf("No Server found with Name: %s", d.MachineName)
	}

	// Retrieve ServerDetails for the given ServerID
	detailed_server_response, err := client.GetServer(d.ServerId)
	if err != nil {
		return err
	}

	// Override instance object with the new unmarshaled detailed server response
	instance = detailed_server_response

	// Wait until instance is ACTIVE
	log.Debugf("Waiting for ArubaCloud Server...", map[string]interface{}{"MachineID": d.ServerId})
	instance, err = d.waitForServerStatus(3)
	if err != nil {
		return err
	}

	// In order to obtain the IP address we have to get the server detail

	// Save Ip address that should be available at this point
	d.IPAddress = ""
	d.IPAddress = ipAddressValue

	if d.IPAddress == "" {
		return fmt.Errorf("No IP found for instance %s", instance.ServerId)
	}

	log.Debugf("IP address found", map[string]interface{}{
		"MachineID": d.ServerId,
		"IP":        d.IPAddress,
	})
	
	return nil
}

// Create a new docker machine instance on ArubaCloud Cloud
func (d *Driver) Create() error {
	switch d.Type{
		case "Smart":
		err := d.CreateSmart()
		if err != nil {
			return err
		}
		case "Pro":
		err := d.CreatePro()
		if err != nil {
			return err
		}
	}
	
	

	// All done !
	return nil
}

func (d *Driver) publicSSHKeyPath() string {
	return d.GetSSHKeyPath() + ".pub"
}

func (d *Driver) createKeyPair() (string, error) {
	log.Debug("Creating Key Pair...", map[string]interface{}{"Name": d.KeyPairName})
	keyfile := d.GetSSHKeyPath()
	keypath := filepath.Dir(keyfile)
	err := os.MkdirAll(keypath, 0700)
	if err != nil {
		return "", err
	}

	err = ssh.GenerateSSHKey(d.GetSSHKeyPath())
	if err != nil {
		return "", err
	}
	
	publicKey, err := ioutil.ReadFile(d.publicSSHKeyPath())
	if err != nil {
		return "", err
	}

	return string(publicKey), nil
}

func (d *Driver) GetSSHHostname() (string, error) {
	log.Debugf("GetSSHHostname: ", d.IPAddress)
	return d.IPAddress, nil
}

func (d *Driver) GetState() (state.State, error) {
	log.Debugf("Get status for ArubaCloud Server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	instance, err := client.GetServer(d.ServerId)
	if err != nil {
		return state.None, err
	}

	log.Debugf("ArubaCloud Server", map[string]interface{}{
		"MachineID": d.ServerId,
		"State":     instance.ServerStatus,
	})

	switch instance.ServerStatus {
	case 3:
		return state.Running, nil
	case 4:
		return state.Saved, nil
	case 2:
		return state.Stopped, nil
	case 1:
		return state.Starting, nil
	}

	return state.None, nil
}

func (d *Driver) Remove() error {
	log.Debugf("deleting server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	// Check the state of the Virtual Machine
	s, err := d.GetState()
	if err != nil { return err }
	if s == state.Running {
		client.StopServer(d.ServerId)
		_, err := d.waitForServerStatus(2)//2 = Stop --3 = Running --5 = Deleted
		if err != nil { return err }
	}

	// Deletes instance
	err = client.DeleteServer(d.ServerId)
	if err != nil {
		return err
	}

	return nil
}

func (d *Driver) Start() error {
	log.Debugf("starting server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	err := client.StartServer(d.ServerId)
	if err != nil {
		return err
	}

	return nil
}

func (d *Driver) Stop() (err error) {
	log.Debugf("Stopping server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	// Check the state of the virtual machine
	s, err := d.GetState()
	if err != nil {
		return err
	}

	// Poweroff VM in case it's running
	if s == state.Running {
		client.StopServer(d.ServerId)
		_, err := d.waitForServerStatus(3)
		if err != nil { return err }
	}

	return nil
}

func (d *Driver) GetURL() (string, error) {
	if d.IPAddress == "" {
		return "", nil
	}
	return fmt.Sprintf("tcp://%s", net.JoinHostPort(d.IPAddress, "2376")), nil
}

func (d *Driver) Restart() error {
	log.Debugf("restarting server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	// Poweroff the VM
	client.StopServer(d.ServerId)
	_, err := d.waitForServerStatus(2)
	if err != nil {
		return err
	}
	// Poweron the VM
	client.StartServer(d.ServerId)
	_, err = d.waitForServerStatus(3)
	if err != nil {
		return err
	}

	return nil
}

func (d *Driver) Kill() (err error) {
	log.Debugf("Killing server...", map[string]interface{}{"MachineID": d.ServerId})

	client := d.getClient()

	// Check the state of the virtual machine
	s, err := d.GetState()
	if err != nil {
		return err
	}

	// Poweroff VM in case it's running
	if s == state.Running {
		client.StopServer(d.ServerId)
		_, err := d.waitForServerStatus(3)
		if err != nil { return err }
	}

	return nil
}
