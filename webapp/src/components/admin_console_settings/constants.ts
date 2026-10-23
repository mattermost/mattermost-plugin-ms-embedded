// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Custom event for input changes
export const EVENT_APP_INPUT_CHANGE = 'com.mattermost.ms-embedded__app_input_change';

// Teams limits the app manifest's name.short to 30 characters
export const APP_NAME_MAX_LENGTH = 30;

// Type for the custom event data
export interface AppInputChangeEvent {
    id: string;
    value: string;
}

// Type for plugin settings
export interface PluginSettings {
    app_id?: string;
    app_name?: string;
    app_version?: string;
    icon_color_path?: string;
    icon_outline_path?: string;
    [key: string]: string | undefined;
}

// Type for config object structure
export interface AdminConsoleConfig {
    PluginSettings?: {
        Plugins?: {
            'com.mattermost.ms-embedded'?: PluginSettings;
            [key: string]: unknown;
        };
        [key: string]: unknown;
    };
    [key: string]: unknown;
}
