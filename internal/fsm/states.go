package fsm

type State string

const (
	StateStart State = "start"

	// Common
	StateAskRole State = "ask_role"

	// Organizer
	StateOrgMenu       State = "org_menu"
	StateNewSanta      State = "org_new_santa"
	StateOrgEnterDes   State = "org_enter_desires"
	StateOrgGroupID    State = "org_group_id"
	StateOrgSendDes    State = "org_send_desires"
	StateOrgStartSanta State = "org_start_santa"

	// Participant
	StateEnterGroupID State = "user_group_id"
	StateEnterDesires State = "user_desires"
)
