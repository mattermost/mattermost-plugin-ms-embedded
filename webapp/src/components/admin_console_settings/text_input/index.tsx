// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {EVENT_APP_INPUT_CHANGE} from '../constants';

interface Props {
    id: string;
    label: string;
    helpText?: React.ReactNode;
    placeholder?: string;
    value: string;
    disabled?: boolean;
    maxLength?: number;
    onChange: (id: string, value: string) => void;
}

// Reusable text input component for app settings
const TextInput: React.FC<Props> = (props) => {
    const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        let newValue = e.target.value;

        // Count Unicode code points (not UTF-16 units) to match server rune limits.
        if (props.maxLength !== undefined) {
            newValue = [...newValue].slice(0, props.maxLength).join('');
        }

        props.onChange(props.id, newValue);

        // Dispatch custom event for real-time validation
        window.dispatchEvent(new CustomEvent(EVENT_APP_INPUT_CHANGE, {
            detail: {
                id: props.id,
                value: newValue,
            },
        }));
    };

    return (
        <div className='form-group'>
            <label className='control-label'>{props.label}</label>
            <div className='help-text'>{props.helpText}</div>
            <div className='col-sm-8'>
                <input
                    id={props.id}
                    className='form-control'
                    type='text'
                    placeholder={props.placeholder}
                    value={props.value}
                    onChange={handleChange}
                    disabled={props.disabled}
                />
            </div>
        </div>
    );
};

export default TextInput;
